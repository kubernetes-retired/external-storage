/*
Copyright 2014 The Kubernetes Authors.

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

package openstack

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/extensions/trusts"
	tokens3 "github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/pagination"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/gcfg.v1"

	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	netutil "k8s.io/apimachinery/pkg/util/net"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/kubernetes/pkg/controller"
)

// ProviderName is the name of the cloud provider
const ProviderName = "openstack"

// ErrNotFound is the error for not found
var ErrNotFound = errors.New("Failed to find object")

// ErrMultipleResults is the error when multiple results obtained but only on expected
var ErrMultipleResults = errors.New("Multiple results where only one expected")

// ErrNoAddressFound is the error when no address found for host
var ErrNoAddressFound = errors.New("No address found for host")

// MyDuration is the encoding.TextUnmarshaler interface for time.Duration
type MyDuration struct {
	time.Duration
}

// UnmarshalText unmarshals the text
func (d *MyDuration) UnmarshalText(text []byte) error {
	res, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = res
	return nil
}

// LoadBalancer is the struct for load balancer
type LoadBalancer struct {
	network *gophercloud.ServiceClient
	compute *gophercloud.ServiceClient
	opts    LoadBalancerOpts
}

// LoadBalancerOpts defines options for load balancer
type LoadBalancerOpts struct {
	LBVersion            string     `gcfg:"lb-version"` // overrides autodetection. v1 or v2
	SubnetID             string     `gcfg:"subnet-id"`  // required
	FloatingNetworkID    string     `gcfg:"floating-network-id"`
	LBMethod             string     `gcfg:"lb-method"`
	CreateMonitor        bool       `gcfg:"create-monitor"`
	MonitorDelay         MyDuration `gcfg:"monitor-delay"`
	MonitorTimeout       MyDuration `gcfg:"monitor-timeout"`
	MonitorMaxRetries    uint       `gcfg:"monitor-max-retries"`
	ManageSecurityGroups bool       `gcfg:"manage-security-groups"`
	NodeSecurityGroupID  string     `gcfg:"node-security-group"`
}

// BlockStorageOpts defines options for block storage
type BlockStorageOpts struct {
	BSVersion       string `gcfg:"bs-version"`        // overrides autodetection. v1 or v2. Defaults to auto
	TrustDevicePath bool   `gcfg:"trust-device-path"` // See Issue #33128
}

// RouterOpts defines options for the router
type RouterOpts struct {
	RouterID string `gcfg:"router-id"` // required
}

// OpenStack is an implementation of cloud provider Interface for OpenStack.
type OpenStack struct {
	provider  *gophercloud.ProviderClient
	region    string
	lbOpts    LoadBalancerOpts
	bsOpts    BlockStorageOpts
	routeOpts RouterOpts
	// InstanceID of the server where this OpenStack object is instantiated.
	localInstanceID string
}

// Config defines config options for the openstack cloud provider
type Config struct {
	Global struct {
		AuthURL    string `gcfg:"auth-url"`
		Username   string
		UserID     string `gcfg:"user-id"`
		Password   string
		TenantID   string `gcfg:"tenant-id"`
		TenantName string `gcfg:"tenant-name"`
		TrustID    string `gcfg:"trust-id"`
		DomainID   string `gcfg:"domain-id"`
		DomainName string `gcfg:"domain-name"`
		Region     string
		CAFile     string `gcfg:"ca-file"`
	}
	LoadBalancer LoadBalancerOpts
	BlockStorage BlockStorageOpts
	Route        RouterOpts
}

func (cfg Config) toAuthOptions() gophercloud.AuthOptions {
	return gophercloud.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthURL,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserID,
		Password:         cfg.Global.Password,
		TenantID:         cfg.Global.TenantID,
		TenantName:       cfg.Global.TenantName,
		DomainID:         cfg.Global.DomainID,
		DomainName:       cfg.Global.DomainName,

		// Persistent service, so we need to be able to renew tokens.
		AllowReauth: true,
	}
}

func (cfg Config) toAuth3Options() tokens3.AuthOptions {
	return tokens3.AuthOptions{
		IdentityEndpoint: cfg.Global.AuthURL,
		Username:         cfg.Global.Username,
		UserID:           cfg.Global.UserID,
		Password:         cfg.Global.Password,
		DomainID:         cfg.Global.DomainID,
		DomainName:       cfg.Global.DomainName,
		AllowReauth:      true,
	}
}

func readConfig(config io.Reader) (Config, error) {
	if config == nil {
		err := fmt.Errorf("no OpenStack cloud provider config file given")
		return Config{}, err
	}

	var cfg Config

	// Set default values for config params
	cfg.BlockStorage.BSVersion = "auto"
	cfg.BlockStorage.TrustDevicePath = false

	err := gcfg.ReadInto(&cfg, config)
	return cfg, err
}

// Caller is Tiny helper for conditional unwind logic
type Caller bool

// NewCaller returns a Caller
func NewCaller() Caller { return Caller(true) }

// Disarm disarms the caller
func (c *Caller) Disarm() { *c = false }

// Call returns a Call
func (c *Caller) Call(f func()) {
	if *c {
		f()
	}
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		cfg, err := readConfig(config)
		if err != nil {
			return nil, err
		}
		return newOpenStack(cfg)
	})
}

func newOpenStack(cfg Config) (*OpenStack, error) {
	provider, err := openstack.NewClient(cfg.Global.AuthURL)
	if err != nil {
		return nil, err
	}
	if cfg.Global.CAFile != "" {
		roots, err := certutil.NewPool(cfg.Global.CAFile)
		if err != nil {
			return nil, err
		}
		config := &tls.Config{}
		config.RootCAs = roots
		provider.HTTPClient.Transport = netutil.SetOldTransportDefaults(&http.Transport{TLSClientConfig: config})

	}
	if cfg.Global.TrustID != "" {
		opts := cfg.toAuth3Options()
		authOptsExt := trusts.AuthOptsExt{
			TrustID:            cfg.Global.TrustID,
			AuthOptionsBuilder: &opts,
		}
		err = openstack.AuthenticateV3(provider, authOptsExt, gophercloud.EndpointOpts{})
	} else {
		err = openstack.Authenticate(provider, cfg.toAuthOptions())
	}

	if err != nil {
		return nil, err
	}

	os := OpenStack{
		provider:        provider,
		region:          cfg.Global.Region,
		lbOpts:          cfg.LoadBalancer,
		bsOpts:          cfg.BlockStorage,
		routeOpts:       cfg.Route,
		localInstanceID: "",
	}

	return &os, nil
}

// LoadBalancer returns a load balancer
func (os *OpenStack) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	glog.V(4).Info("openstack.LoadBalancer() called")

	// TODO: Search for and support Rackspace loadbalancer API, and others.
	network, err := os.NewNetworkV2()
	if err != nil {
		return nil, false
	}

	compute, err := os.NewComputeV2()
	if err != nil {
		return nil, false
	}

	lbVersion := os.lbOpts.LBVersion
	if lbVersion == "" {
		// No version specified, try newest supported by server
		netExts, err := networkExtensions(network)
		if err != nil {
			glog.Warningf("Failed to list neutron extensions: %v", err)
			return nil, false
		}

		if netExts["lbaasv2"] {
			lbVersion = "v2"
		} else if netExts["lbaas"] {
			lbVersion = "v1"
		} else {
			glog.Warningf("Failed to find neutron LBaaS extension (v1 or v2)")
			return nil, false
		}
		glog.V(3).Infof("Using LBaaS extension %v", lbVersion)
	}

	glog.V(1).Info("Claiming to support LoadBalancer")

	if lbVersion == "v2" {
		return &LbaasV2{LoadBalancer{network, compute, os.lbOpts}}, true
	} else if lbVersion == "v1" {
		return &LbaasV1{LoadBalancer{network, compute, os.lbOpts}}, true
	} else {
		glog.Warningf("Config error: unrecognised lb-version \"%v\"", lbVersion)
		return nil, false
	}
}

// ScrubDNS filters DNS settings for pods.
func (os *OpenStack) ScrubDNS(nameServers, searches []string) ([]string, []string) {
	return nameServers, searches
}

// Initialize passes a Kubernetes clientBuilder interface to the cloud provider
func (os *OpenStack) Initialize(clientBuilder controller.ControllerClientBuilder) {}

// mapNodeNameToServerName maps a k8s NodeName to an OpenStack Server Name
// This is a simple string cast.
func mapNodeNameToServerName(nodeName types.NodeName) string {
	return string(nodeName)
}

// mapServerToNodeName maps an OpenStack Server to a k8s NodeName
func mapServerToNodeName(server *servers.Server) types.NodeName {
	// Node names are always lowercase, and (at least)
	// routecontroller does case-sensitive string comparisons
	// assuming this
	return types.NodeName(strings.ToLower(server.Name))
}

func foreachServer(client *gophercloud.ServiceClient, opts servers.ListOptsBuilder, handler func(*servers.Server) (bool, error)) error {
	pager := servers.List(client, opts)

	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		s, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		for _, server := range s {
			ok, err := handler(&server)
			if !ok || err != nil {
				return false, err
			}
		}
		return true, nil
	})
	return err
}

func getServerByName(client *gophercloud.ServiceClient, name types.NodeName) (*servers.Server, error) {
	opts := servers.ListOpts{
		Name:   fmt.Sprintf("^%s$", regexp.QuoteMeta(mapNodeNameToServerName(name))),
		Status: "ACTIVE",
	}
	pager := servers.List(client, opts)

	serverList := make([]servers.Server, 0, 1)

	err := pager.EachPage(func(page pagination.Page) (bool, error) {
		s, err := servers.ExtractServers(page)
		if err != nil {
			return false, err
		}
		serverList = append(serverList, s...)
		if len(serverList) > 1 {
			return false, ErrMultipleResults
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	if len(serverList) == 0 {
		return nil, ErrNotFound
	}

	return &serverList[0], nil
}

func addToNodeAddresses(addresses *[]v1.NodeAddress, addAddresses ...v1.NodeAddress) {
	for _, add := range addAddresses {
		exists := false
		for _, existing := range *addresses {
			if existing.Address == add.Address && existing.Type == add.Type {
				exists = true
				break
			}
		}
		if !exists {
			*addresses = append(*addresses, add)
		}
	}
}

func nodeAddresses(srv *servers.Server) ([]v1.NodeAddress, error) {
	addrs := []v1.NodeAddress{}

	type Address struct {
		IPType string `mapstructure:"OS-EXT-IPS:type"`
		Addr   string
	}

	var addresses map[string][]Address
	err := mapstructure.Decode(srv.Addresses, &addresses)
	if err != nil {
		return nil, err
	}

	for network, addrList := range addresses {
		for _, props := range addrList {
			var addressType v1.NodeAddressType
			if props.IPType == "floating" || network == "public" {
				addressType = v1.NodeExternalIP
			} else {
				addressType = v1.NodeInternalIP
			}

			addToNodeAddresses(&addrs,
				v1.NodeAddress{
					Type:    addressType,
					Address: props.Addr,
				},
			)
		}
	}

	// AccessIPs are usually duplicates of "public" addresses.
	if srv.AccessIPv4 != "" {
		addToNodeAddresses(&addrs,
			v1.NodeAddress{
				Type:    v1.NodeExternalIP,
				Address: srv.AccessIPv4,
			},
		)
	}

	if srv.AccessIPv6 != "" {
		addToNodeAddresses(&addrs,
			v1.NodeAddress{
				Type:    v1.NodeExternalIP,
				Address: srv.AccessIPv6,
			},
		)
	}

	return addrs, nil
}

func getAddressesByName(client *gophercloud.ServiceClient, name types.NodeName) ([]v1.NodeAddress, error) {
	srv, err := getServerByName(client, name)
	if err != nil {
		return nil, err
	}

	return nodeAddresses(srv)
}

func getAddressByName(client *gophercloud.ServiceClient, name types.NodeName) (string, error) {
	addrs, err := getAddressesByName(client, name)
	if err != nil {
		return "", err
	} else if len(addrs) == 0 {
		return "", ErrNoAddressFound
	}

	for _, addr := range addrs {
		if addr.Type == v1.NodeInternalIP {
			return addr.Address, nil
		}
	}

	return addrs[0].Address, nil
}

// Clusters return cloud provider clusters
func (os *OpenStack) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

// ProviderName returns the cloud provider ID.
func (os *OpenStack) ProviderName() string {
	return ProviderName
}

func isNotFound(err error) bool {
	e, ok := err.(*gophercloud.ErrUnexpectedResponseCode)
	return ok && e.Actual == http.StatusNotFound
}

func (os *OpenStack) volumeService(forceVersion string) (volumeService, error) {
	sClient, err := os.NewBlockStorageV2()
	if err != nil {
		return nil, err
	}
	return &VolumesV2{sClient, os.bsOpts}, nil
}

func (os *OpenStack) snapshotService() (snapshotService, error) {
	// Currently V2 is the only valid option, will add V3 option when available
	// in gophercloud
	sClient, err := os.NewBlockStorageV2()
	if err != nil {
		return nil, err
	}
	return &SnapshotsV2{sClient, os.bsOpts}, nil
}

// Routes returns cloud provider routes
func (os *OpenStack) Routes() (cloudprovider.Routes, bool) {
	glog.V(4).Info("openstack.Routes() called")

	network, err := os.NewNetworkV2()
	if err != nil {
		return nil, false
	}

	netExts, err := networkExtensions(network)
	if err != nil {
		glog.Warningf("Failed to list neutron extensions: %v", err)
		return nil, false
	}

	if !netExts["extraroute"] {
		glog.V(3).Infof("Neutron extraroute extension not found, required for Routes support")
		return nil, false
	}

	compute, err := os.NewComputeV2()
	if err != nil {
		return nil, false
	}

	r, err := NewRoutes(compute, network, os.routeOpts)
	if err != nil {
		glog.Warningf("Error initialising Routes support: %v", err)
		return nil, false
	}

	glog.V(1).Info("Claiming to support Routes")

	return r, true
}

// Zones returns cloud provider zones
func (os *OpenStack) Zones() (cloudprovider.Zones, bool) {
	glog.V(1).Info("Claiming to support Zones")

	return os, true
}

// GetZone gets a zone from cloud provider
func (os *OpenStack) GetZone() (cloudprovider.Zone, error) {
	md, err := getMetadata()
	if err != nil {
		return cloudprovider.Zone{}, err
	}

	zone := cloudprovider.Zone{
		FailureDomain: md.AvailabilityZone,
		Region:        os.region,
	}
	glog.V(1).Infof("Current zone is %v", zone)

	return zone, nil
}
