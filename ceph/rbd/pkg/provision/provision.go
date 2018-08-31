/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provision

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/miekg/dns"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/volume"
)

const (
	// ProvisionerName is a unique string to represent this volume provisioner. This value will be
	// added in PV annotations under 'pv.kubernetes.io/provisioned-by' key.
	ProvisionerName = "ceph.com/rbd"
	// Each provisioner have a identify string to distinguish with others. This
	// identify string will be added in PV annoations under this key.
	provisionerIDAnn = "rbdProvisionerIdentity"

	secretKeyName   = "key" // key name used in secret
	rbdImageFormat1 = "1"
	rbdImageFormat2 = "2"
)

var (
	supportedFeatures = sets.NewString("layering")
)

// rbdProvisionOptions is internal representation of rbd provision options,
// parsed out from storage class parameters.
// https://github.com/kubernetes/website/blob/master/docs/concepts/storage/storage-classes.md#ceph-rbd
type rbdProvisionOptions struct {
	// Ceph monitors.
	monitors []string
	// Ceph RBD pool. Default is "rbd".
	pool string
	// Ceph client ID that is capable of creating images in the pool. Default is "admin".
	adminID string
	// Secret of admin client ID.
	adminSecret string
	// Ceph client ID that is used to map the RBD image. Default is the same as admin client ID.
	userID string
	// The name of Ceph Secret for userID to map RBD image. This parameter is required.
	userSecretName string
	// The namespace of Ceph Secret for userID to map RBD image. This parameter is optional.
	userSecretNamespace string
	// fsType that is supported by kubernetes. Default: "ext4".
	fsType string
	// Ceph RBD image format, "1" or "2". Default is "1".
	imageFormat string
	// This parameter is optional and should only be used if you set
	// imageFormat to "2". Currently supported features are layering only.
	// Default is "", and no features are turned on.
	imageFeatures []string
}

type rbdProvisioner struct {
	// Kubernetes Client. Use to retrieve Ceph admin secret
	client kubernetes.Interface
	// Identity of this rbdProvisioner, generated. Used to identify "this"
	// provisioner's PVs.
	identity string
	rbdUtil  *RBDUtil
	dnsip    string
}

// NewRBDProvisioner creates a Provisioner that provisions Ceph RBD PVs backed by Ceph RBD images.
func NewRBDProvisioner(client kubernetes.Interface, id string) controller.Provisioner {
	return &rbdProvisioner{
		client:   client,
		identity: id,
		rbdUtil:  &RBDUtil{},
	}
}

var _ controller.Provisioner = &rbdProvisioner{}

// getAccessModes returns access modes RBD volume supported.
func (p *rbdProvisioner) getAccessModes() []v1.PersistentVolumeAccessMode {
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
		v1.ReadOnlyMany,
	}
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *rbdProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if !util.AccessModesContainedInAll(p.getAccessModes(), options.PVC.Spec.AccessModes) {
		return nil, fmt.Errorf("invalid AccessModes %v: only AccessModes %v are supported", options.PVC.Spec.AccessModes, p.getAccessModes())
	}
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim Selector is not supported")
	}
	opts, err := p.parseParameters(options.Parameters)
	if err != nil {
		return nil, err
	}
	// create random image name
	image := fmt.Sprintf("kubernetes-dynamic-pvc-%s", uuid.NewUUID())
	rbd, sizeMB, err := p.rbdUtil.CreateImage(image, opts, options)
	if err != nil {
		glog.Errorf("rbd: create volume failed, err: %v", err)
		return nil, err
	}
	glog.Infof("successfully created rbd image %q", image)

	rbd.SecretRef = new(v1.SecretReference)
	rbd.SecretRef.Name = opts.userSecretName
	if len(opts.userSecretNamespace) > 0 {
		rbd.SecretRef.Namespace = opts.userSecretNamespace
	}
	rbd.RadosUser = opts.userID

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
			Annotations: map[string]string{
				provisionerIDAnn: p.identity,
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			MountOptions:                  options.MountOptions,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): resource.MustParse(fmt.Sprintf("%dMi", sizeMB)),
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				RBD: rbd,
			},
		},
	}
	// use default access modes if missing
	if len(pv.Spec.AccessModes) == 0 {
		glog.Warningf("no access modes specified, use default: %v", p.getAccessModes())
		pv.Spec.AccessModes = p.getAccessModes()
	}

	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *rbdProvisioner) Delete(volume *v1.PersistentVolume) error {
	// TODO: Should we check `pv.kubernetes.io/provisioned-by` key too?
	ann, ok := volume.Annotations[provisionerIDAnn]
	if !ok {
		return errors.New("identity annotation not found on PV")
	}
	if ann != p.identity {
		return &controller.IgnoredError{Reason: "identity annotation on PV does not match ours"}
	}

	class, err := p.client.StorageV1beta1().StorageClasses().Get(helper.GetPersistentVolumeClass(volume), metav1.GetOptions{})
	if err != nil {
		return err
	}
	opts, err := p.parseParameters(class.Parameters)
	if err != nil {
		return err
	}
	image := volume.Spec.PersistentVolumeSource.RBD.RBDImage
	return p.rbdUtil.DeleteImage(image, opts)
}

// Look up the cluster dns service by label "coredns", falling back to "kube-dns" if not found
func findDNSIP(p *rbdProvisioner) (dnsip string) {
	// find DNS server address through client API
	// cache result in rbdProvisioner
	var dnssvc *v1.Service

	if p.dnsip == "" {
		coredns, err := p.client.CoreV1().Services(metav1.NamespaceSystem).Get("coredns", metav1.GetOptions{})

		if err != nil {
			glog.Warningf("error getting coredns service: %v. Falling back to kube-dns\n", err)
			kubedns, err := p.client.CoreV1().Services(metav1.NamespaceSystem).Get("kube-dns", metav1.GetOptions{})
			if err != nil {
				glog.Errorf("error getting kube-dns service: %v\n", err)
				return ""
			}
			dnssvc = kubedns
		} else {
			dnssvc = coredns
		}

		if len(dnssvc.Spec.ClusterIP) == 0 {
			glog.Errorf("DNS service ClusterIP bad\n")
			return ""
		}

		p.dnsip = dnssvc.Spec.ClusterIP
	}

	return p.dnsip
}

// Look up hostname in dns server serverip.
func lookuphost(hostname string, serverip string) (iplist []string, err error) {
	glog.V(4).Infof("lookuphost %q on %q\n", hostname, serverip)
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(hostname), dns.TypeA)
	in, err := dns.Exchange(m, joinHostPort(serverip, "53"))
	if err != nil {
		glog.Errorf("dns lookup of %q failed: err %v", hostname, err)
		return nil, err
	}
	for _, a := range in.Answer {
		glog.V(4).Infof("lookuphost answer: %v\n", a)
		if t, ok := a.(*dns.A); ok {
			iplist = append(iplist, t.A.String())
		}
	}

	return iplist, nil
}

func splitHostPort(hostport string) (host, port string) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, ""
	}
	return host, port
}

func joinHostPort(host, port string) (hostport string) {
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	return host
}

func (p *rbdProvisioner) parseParameters(parameters map[string]string) (*rbdProvisionOptions, error) {
	// options with default values
	opts := &rbdProvisionOptions{
		pool:        "rbd",
		adminID:     "admin",
		imageFormat: rbdImageFormat1,
	}

	var (
		err                  error
		adminSecretName      = ""
		adminSecretNamespace = "default"
	)

	for k, v := range parameters {
		switch strings.ToLower(k) {
		case "monitors":
			// Try to find DNS info in local cluster DNS so that the kubernetes
			// host DNS config doesn't have to know about cluster DNS
			dnsip := findDNSIP(p)
			glog.V(4).Infof("dnsip: %q\n", dnsip)
			arr := strings.Split(v, ",")
			for _, m := range arr {
				mhost, mport := splitHostPort(m)
				if dnsip != "" && net.ParseIP(mhost) == nil {
					var lookup []string
					if lookup, err = lookuphost(mhost, dnsip); err == nil {
						for _, a := range lookup {
							glog.V(1).Infof("adding %+v from mon lookup\n", a)
							opts.monitors = append(opts.monitors, joinHostPort(a, mport))
						}
					} else {
						opts.monitors = append(opts.monitors, joinHostPort(mhost, mport))
					}
				} else {
					opts.monitors = append(opts.monitors, joinHostPort(mhost, mport))
				}
			}
			glog.V(4).Infof("final monitors list: %v\n", opts.monitors)
			if len(opts.monitors) < 1 {
				return nil, fmt.Errorf("missing Ceph monitors")
			}
		case "adminid":
			if v == "" {
				// keep consistent behavior with in-tree rbd provisioner, which use default value if user provides empty string
				// TODO: treat empty string invalid value?
				v = "admin"
			}
			opts.adminID = v
		case "adminsecretname":
			adminSecretName = v
		case "adminsecretnamespace":
			adminSecretNamespace = v
		case "userid":
			opts.userID = v
		case "pool":
			if v == "" {
				// keep consistent behavior with in-tree rbd provisioner, which use default value if user provides empty string
				// TODO: treat empty string invalid value?
				v = "rbd"
			}
			opts.pool = v
		case "usersecretname":
			if v == "" {
				return nil, fmt.Errorf("missing user secret name")
			}
			opts.userSecretName = v
		case "usersecretnamespace":
			opts.userSecretNamespace = v
		case "imageformat":
			if v != rbdImageFormat1 && v != rbdImageFormat2 {
				return nil, fmt.Errorf("invalid ceph imageformat %s, expecting %s or %s", v, rbdImageFormat1, rbdImageFormat2)
			}
			opts.imageFormat = v
		case "imagefeatures":
			arr := strings.Split(v, ",")
			for _, f := range arr {
				if !supportedFeatures.Has(f) {
					return nil, fmt.Errorf("invalid feature %q for %s provisioner, supported features are: %v", f, ProvisionerName, supportedFeatures)
				}
				opts.imageFeatures = append(opts.imageFeatures, f)
			}
		case volume.VolumeParameterFSType:
			opts.fsType = v
		default:
			return nil, fmt.Errorf("invalid option %q for %s provisioner", k, ProvisionerName)
		}
	}

	// find adminSecret
	var secret string
	if adminSecretName == "" {
		return nil, fmt.Errorf("missing Ceph admin secret name")
	}
	if secret, err = p.parsePVSecret(adminSecretNamespace, adminSecretName); err != nil {
		return nil, fmt.Errorf("failed to get admin secret from [%q/%q]: %v", adminSecretNamespace, adminSecretName, err)
	}
	opts.adminSecret = secret

	// set user ID to admin ID if empty
	if opts.userID == "" {
		opts.userID = opts.adminID
	}

	return opts, nil
}

// parsePVSecret retrives secret value for a given namespace and name.
func (p *rbdProvisioner) parsePVSecret(namespace, secretName string) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("Cannot get kube client")
	}
	secrets, err := p.client.CoreV1().Secrets(namespace).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	// TODO: Should we check secret.Type, like `k8s.io/kubernetes/pkg/volume/util.GetSecretForPV` function?
	secret := ""
	for k, v := range secrets.Data {
		if k == secretKeyName {
			return string(v), nil
		}
		secret = string(v)
	}

	// If not found, the last secret in the map wins as done before
	return secret, nil
}
