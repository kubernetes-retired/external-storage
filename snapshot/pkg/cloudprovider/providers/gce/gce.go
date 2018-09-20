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

package gce

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/gcfg.v1"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/flowcontrol"
	apiservice "k8s.io/kubernetes/pkg/api/v1/service"
	netsets "k8s.io/kubernetes/pkg/util/net/sets"
	"k8s.io/kubernetes/pkg/volume"

	"cloud.google.com/go/compute/metadata"
	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/kubernetes/pkg/kubelet/apis"
)

const (
	// ProviderName is the cloud provider name
	ProviderName = "gce"

	k8sNodeRouteTag = "k8s-node-route"

	// AffinityTypeNone - no session affinity.
	gceAffinityTypeNone = "NONE"
	// AffinityTypeClientIP - affinity based on Client IP.
	gceAffinityTypeClientIP = "CLIENT_IP"

	operationPollInterval = 3 * time.Second
	// Creating Route in very large clusters, may take more than half an hour.
	operationPollTimeoutDuration = time.Hour

	// Each page can have 500 results, but we cap how many pages
	// are iterated through to prevent infinite loops if the API
	// were to continuously return a nextPageToken.
	maxPages = 25

	maxTargetPoolCreateInstances = 200

	// HTTP Load Balancer parameters
	// Configure 2 second period for external health checks.
	gceHcCheckIntervalSeconds = int64(2)
	gceHcTimeoutSeconds       = int64(1)
	// Start sending requests as soon as a pod is found on the node.
	gceHcHealthyThreshold = int64(1)
	// Defaults to 5 * 2 = 10 seconds before the LB will steer traffic away
	gceHcUnhealthyThreshold = int64(5)
)

// Cloud is an implementation of Interface, LoadBalancer and Instances for Google Compute Engine.
type Cloud struct {
	service                  *compute.Service
	containerService         *container.Service
	projectID                string
	region                   string
	localZone                string   // The zone in which we are running
	managedZones             []string // List of zones we are spanning (for multi-AZ clusters, primarily when running on master)
	networkURL               string
	nodeTags                 []string // List of tags to use on firewall rules for load balancers
	nodeInstancePrefix       string   // If non-"", an advisory prefix for all nodes in the cluster
	useMetadataServer        bool
	operationPollRateLimiter flowcontrol.RateLimiter
}

// Config contains config options for the GCE Cloud
type Config struct {
	Global struct {
		TokenURL           string   `gcfg:"token-url"`
		TokenBody          string   `gcfg:"token-body"`
		ProjectID          string   `gcfg:"project-id"`
		NetworkName        string   `gcfg:"network-name"`
		NodeTags           []string `gcfg:"node-tags"`
		NodeInstancePrefix string   `gcfg:"node-instance-prefix"`
		Multizone          bool     `gcfg:"multizone"`
	}
}

// DiskType defines disk types
type DiskType string

const (
	// DiskTypeSSD is the disk type for SSD PD
	DiskTypeSSD = "pd-ssd"
	// DiskTypeStandard is the disk type for standard PD
	DiskTypeStandard = "pd-standard"

	diskTypeDefault     = DiskTypeStandard
	diskTypeURITemplate = "https://www.googleapis.com/compute/v1/projects/%s/zones/%s/diskTypes/%s"
)

// Disks is interface for manipulation with GCE PDs.
type Disks interface {
	// AttachDisk attaches given disk to the node with the specified NodeName.
	// Current instance is used when instanceID is empty string.
	AttachDisk(diskName string, nodeName types.NodeName, readOnly bool) error

	// DetachDisk detaches given disk to the node with the specified NodeName.
	// Current instance is used when nodeName is empty string.
	DetachDisk(devicePath string, nodeName types.NodeName) error

	// DiskIsAttached checks if a disk is attached to the node with the specified NodeName.
	DiskIsAttached(diskName string, nodeName types.NodeName) (bool, error)

	// DisksAreAttached is a batch function to check if a list of disks are attached
	// to the node with the specified NodeName.
	DisksAreAttached(diskNames []string, nodeName types.NodeName) (map[string]bool, error)

	// CreateDisk creates a new PD with given properties. Tags are serialized
	// as JSON into Description field.
	CreateDisk(name string, diskType string, zone string, sizeGb int64, tags map[string]string) error

	// DeleteDisk deletes PD.
	DeleteDisk(diskToDelete string) error

	// Create an GCE PD volume snapshot
	CreateSnapshot(diskName string, zone string, snapshotName string, tags map[string]string) error
	// Delete an GCE PD volume snapshot
	DeleteSnapshot(snapshotToDelete string) error

	// Describe a GCE PD volume snapshot status for create or delete.
	// return status (completed or pending or error), and error
	DescribeSnapshot(snapshotToGet string) (status string, isCompleted bool, err error)

	// Find snapshot by tags
	FindSnapshot(tags map[string]string) ([]string, []string, error)

	// GetAutoLabelsForPD returns labels to apply to PersistentVolume
	// representing this PD, namely failure domain and zone.
	// zone can be provided to specify the zone for the PD,
	// if empty all managed zones will be searched.
	GetAutoLabelsForPD(name string, zone string) (map[string]string, error)
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) { return newGCECloud(config) })
}

// GetComputeService gets the compute service
// Raw access to the underlying GCE service, probably should only be used for e2e tests
func (gce *Cloud) GetComputeService() *compute.Service {
	return gce.service
}

func getProjectAndZone() (string, string, error) {
	result, err := metadata.Get("instance/zone")
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(result, "/")
	if len(parts) != 4 {
		return "", "", fmt.Errorf("unexpected response: %s", result)
	}
	zone := parts[3]
	projectID, err := metadata.ProjectID()
	if err != nil {
		return "", "", err
	}
	return projectID, zone, nil
}

func getInstanceIDViaMetadata() (string, error) {
	result, err := metadata.Get("instance/hostname")
	if err != nil {
		return "", err
	}
	parts := strings.Split(result, ".")
	if len(parts) == 0 {
		return "", fmt.Errorf("unexpected response: %s", result)
	}
	return parts[0], nil
}

func getCurrentExternalIDViaMetadata() (string, error) {
	externalID, err := metadata.Get("instance/id")
	if err != nil {
		return "", fmt.Errorf("couldn't get external ID: %v", err)
	}
	return externalID, nil
}

func getCurrentMachineTypeViaMetadata() (string, error) {
	mType, err := metadata.Get("instance/machine-type")
	if err != nil {
		return "", fmt.Errorf("couldn't get machine type: %v", err)
	}
	parts := strings.Split(mType, "/")
	if len(parts) != 4 {
		return "", fmt.Errorf("unexpected response for machine type: %s", mType)
	}

	return parts[3], nil
}

func getNetworkNameViaMetadata() (string, error) {
	result, err := metadata.Get("instance/network-interfaces/0/network")
	if err != nil {
		return "", err
	}
	parts := strings.Split(result, "/")
	if len(parts) != 4 {
		return "", fmt.Errorf("unexpected response: %s", result)
	}
	return parts[3], nil
}

func getNetworkNameViaAPICall(svc *compute.Service, projectID string) (string, error) {
	// TODO: use PageToken to list all not just the first 500
	networkList, err := svc.Networks.List(projectID).Do()
	if err != nil {
		return "", err
	}

	if networkList == nil || len(networkList.Items) <= 0 {
		return "", fmt.Errorf("GCE Network List call returned no networks for project %q", projectID)
	}

	return networkList.Items[0].Name, nil
}

func getZonesForRegion(svc *compute.Service, projectID, region string) ([]string, error) {
	// TODO: use PageToken to list all not just the first 500
	listCall := svc.Zones.List(projectID)

	// Filtering by region doesn't seem to work
	// (tested in https://cloud.google.com/compute/docs/reference/latest/zones/list)
	// listCall = listCall.Filter("region eq " + region)

	res, err := listCall.Do()
	if err != nil {
		return nil, fmt.Errorf("unexpected response listing zones: %v", err)
	}
	zones := []string{}
	for _, zone := range res.Items {
		regionName := lastComponent(zone.Region)
		if regionName == region {
			zones = append(zones, zone.Name)
		}
	}
	return zones, nil
}

// newGCECloud creates a new instance of GCECloud.
func newGCECloud(config io.Reader) (*Cloud, error) {
	projectID, zone, err := getProjectAndZone()
	if err != nil {
		return nil, err
	}

	region, err := GetGCERegion(zone)
	if err != nil {
		return nil, err
	}

	networkName, err := getNetworkNameViaMetadata()
	if err != nil {
		return nil, err
	}
	networkURL := gceNetworkURL(projectID, networkName)

	// By default, Kubernetes clusters only run against one zone
	managedZones := []string{zone}

	tokenSource := google.ComputeTokenSource("")
	var nodeTags []string
	var nodeInstancePrefix string
	if config != nil {
		var cfg Config
		if err := gcfg.ReadInto(&cfg, config); err != nil {
			glog.Errorf("Couldn't read config: %v", err)
			return nil, err
		}
		glog.Infof("Using GCE provider config %+v", cfg)
		if cfg.Global.ProjectID != "" {
			projectID = cfg.Global.ProjectID
		}
		if cfg.Global.NetworkName != "" {
			if strings.Contains(cfg.Global.NetworkName, "/") {
				networkURL = cfg.Global.NetworkName
			} else {
				networkURL = gceNetworkURL(cfg.Global.ProjectID, cfg.Global.NetworkName)
			}
		}
		if cfg.Global.TokenURL != "" {
			tokenSource = NewAltTokenSource(cfg.Global.TokenURL, cfg.Global.TokenBody)
		}
		nodeTags = cfg.Global.NodeTags
		nodeInstancePrefix = cfg.Global.NodeInstancePrefix
		if cfg.Global.Multizone {
			managedZones = nil // Use all zones in region
		}
	}

	return CreateGCECloud(projectID, region, zone, managedZones, networkURL, nodeTags, nodeInstancePrefix, tokenSource, true /* useMetadataServer */)
}

// CreateGCECloud creates a GCE Cloud object using the specified parameters.
// If no networkUrl is specified, loads networkName via rest call.
// If no tokenSource is specified, uses oauth2.DefaultTokenSource.
// If managedZones is nil / empty all zones in the region will be managed.
func CreateGCECloud(projectID, region, zone string, managedZones []string, networkURL string, nodeTags []string, nodeInstancePrefix string, tokenSource oauth2.TokenSource, useMetadataServer bool) (*Cloud, error) {
	if tokenSource == nil {
		var err error
		tokenSource, err = google.DefaultTokenSource(
			oauth2.NoContext,
			compute.CloudPlatformScope,
			compute.ComputeScope)
		glog.Infof("Using DefaultTokenSource %#v", tokenSource)
		if err != nil {
			return nil, err
		}
	} else {
		glog.Infof("Using existing Token Source %#v", tokenSource)
	}

	if err := wait.PollImmediate(5*time.Second, 30*time.Second, func() (bool, error) {
		if _, err := tokenSource.Token(); err != nil {
			glog.Errorf("error fetching initial token: %v", err)
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}

	client := oauth2.NewClient(oauth2.NoContext, tokenSource)
	svc, err := compute.New(client)
	if err != nil {
		return nil, err
	}

	containerSvc, err := container.New(client)
	if err != nil {
		return nil, err
	}

	if networkURL == "" {
		networkName, err := getNetworkNameViaAPICall(svc, projectID)
		if err != nil {
			return nil, err
		}
		networkURL = gceNetworkURL(projectID, networkName)
	}

	if len(managedZones) == 0 {
		managedZones, err = getZonesForRegion(svc, projectID, region)
		if err != nil {
			return nil, err
		}
	}
	if len(managedZones) != 1 {
		glog.Infof("managing multiple zones: %v", managedZones)
	}

	operationPollRateLimiter := flowcontrol.NewTokenBucketRateLimiter(10, 100) // 10 qps, 100 bucket size.

	return &Cloud{
		service:                  svc,
		containerService:         containerSvc,
		projectID:                projectID,
		region:                   region,
		localZone:                zone,
		managedZones:             managedZones,
		networkURL:               networkURL,
		nodeTags:                 nodeTags,
		nodeInstancePrefix:       nodeInstancePrefix,
		useMetadataServer:        useMetadataServer,
		operationPollRateLimiter: operationPollRateLimiter,
	}, nil
}

// Clusters returns GCE clusters
func (gce *Cloud) Clusters() (cloudprovider.Clusters, bool) {
	return gce, true
}

// ProviderName returns the cloud provider ID.
func (gce *Cloud) ProviderName() string {
	return ProviderName
}

// Known-useless DNS search path.
var uselessDNSSearchRE = regexp.MustCompile(`^[0-9]+.google.internal.$`)

// ScrubDNS filters DNS settings for pods.
func (gce *Cloud) ScrubDNS(nameservers, searches []string) (nsOut, srchOut []string) {
	// GCE has too many search paths by default. Filter the ones we know are useless.
	for _, s := range searches {
		if !uselessDNSSearchRE.MatchString(s) {
			srchOut = append(srchOut, s)
		}
	}
	return nameservers, srchOut
}

// LoadBalancer returns an implementation of LoadBalancer for Google Compute Engine.
func (gce *Cloud) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return gce, true
}

// Instances returns an implementation of Instances for Google Compute Engine.
func (gce *Cloud) Instances() (cloudprovider.Instances, bool) {
	return gce, true
}

// Zones returns an implementation of Zones for Google Compute Engine.
func (gce *Cloud) Zones() (cloudprovider.Zones, bool) {
	return gce, true
}

// Routes returns an implementation of Routes for Google Compute Engine.
func (gce *Cloud) Routes() (cloudprovider.Routes, bool) {
	return gce, true
}

func makeHostURL(projectID, zone, host string) string {
	host = canonicalizeInstanceName(host)
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s",
		projectID, zone, host)
}

func (h *gceInstance) makeComparableHostPath() string {
	return fmt.Sprintf("/zones/%s/instances/%s", h.Zone, h.Name)
}

func hostURLToComparablePath(hostURL string) string {
	idx := strings.Index(hostURL, "/zones/")
	if idx < 0 {
		return ""
	}
	return hostURL[idx:]
}

func (gce *Cloud) targetPoolURL(name, region string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/targetPools/%s", gce.projectID, region, name)
}

func (gce *Cloud) waitForOp(op *compute.Operation, getOperation func(operationName string) (*compute.Operation, error)) error {
	if op == nil {
		return fmt.Errorf("operation must not be nil")
	}

	if opIsDone(op) {
		return getErrorFromOp(op)
	}

	opStart := time.Now()
	opName := op.Name
	return wait.Poll(operationPollInterval, operationPollTimeoutDuration, func() (bool, error) {
		start := time.Now()
		gce.operationPollRateLimiter.Accept()
		duration := time.Now().Sub(start)
		if duration > 5*time.Second {
			glog.Infof("pollOperation: throttled %v for %v", duration, opName)
		}
		pollOp, err := getOperation(opName)
		if err != nil {
			glog.Warningf("GCE poll operation %s failed: pollOp: [%v] err: [%v] getErrorFromOp: [%v]", opName, pollOp, err, getErrorFromOp(pollOp))
		}
		done := opIsDone(pollOp)
		if done {
			duration := time.Now().Sub(opStart)
			if duration > 1*time.Minute {
				// Log the JSON. It's cleaner than the %v structure.
				enc, err := pollOp.MarshalJSON()
				if err != nil {
					glog.Warningf("waitForOperation: long operation (%v): %v (failed to encode to JSON: %v)", duration, pollOp, err)
				} else {
					glog.Infof("waitForOperation: long operation (%v): %v", duration, string(enc))
				}
			}
		}
		return done, getErrorFromOp(pollOp)
	})
}

func opIsDone(op *compute.Operation) bool {
	return op != nil && op.Status == "DONE"
}

func getErrorFromOp(op *compute.Operation) error {
	if op != nil && op.Error != nil && len(op.Error.Errors) > 0 {
		err := &googleapi.Error{
			Code:    int(op.HttpErrorStatusCode),
			Message: op.Error.Errors[0].Message,
		}
		glog.Errorf("GCE operation failed: %v", err)
		return err
	}

	return nil
}

func (gce *Cloud) waitForGlobalOp(op *compute.Operation) error {
	return gce.waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.GlobalOperations.Get(gce.projectID, operationName).Do()
	})
}

func (gce *Cloud) waitForRegionOp(op *compute.Operation, region string) error {
	return gce.waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.RegionOperations.Get(gce.projectID, region, operationName).Do()
	})
}

func (gce *Cloud) waitForZoneOp(op *compute.Operation, zone string) error {
	return gce.waitForOp(op, func(operationName string) (*compute.Operation, error) {
		return gce.service.ZoneOperations.Get(gce.projectID, zone, operationName).Do()
	})
}

// GetLoadBalancer is an implementation of LoadBalancer.GetLoadBalancer
func (gce *Cloud) GetLoadBalancer(clusterName string, service *v1.Service) (*v1.LoadBalancerStatus, bool, error) {
	loadBalancerName := cloudprovider.GetLoadBalancerName(service)
	fwd, err := gce.service.ForwardingRules.Get(gce.projectID, gce.region, loadBalancerName).Do()
	if err == nil {
		status := &v1.LoadBalancerStatus{}
		status.Ingress = []v1.LoadBalancerIngress{{IP: fwd.IPAddress}}

		return status, true, nil
	}
	if isHTTPErrorCode(err, http.StatusNotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func isHTTPErrorCode(err error, code int) bool {
	apiErr, ok := err.(*googleapi.Error)
	return ok && apiErr.Code == code
}

func nodeNames(nodes []*v1.Node) []string {
	ret := make([]string, len(nodes))
	for i, node := range nodes {
		ret[i] = node.Name
	}
	return ret
}

// EnsureLoadBalancer is an implementation of LoadBalancer.EnsureLoadBalancer.
// Our load balancers in GCE consist of four separate GCE resources - a static
// IP address, a firewall rule, a target pool, and a forwarding rule. This
// function has to manage all of them.
// Due to an interesting series of design decisions, this handles both creating
// new load balancers and updating existing load balancers, recognizing when
// each is needed.
func (gce *Cloud) EnsureLoadBalancer(clusterName string, apiService *v1.Service, nodes []*v1.Node) (*v1.LoadBalancerStatus, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("Cannot EnsureLoadBalancer() with no hosts")
	}

	hostNames := nodeNames(nodes)
	hosts, err := gce.getInstancesByNames(hostNames)
	if err != nil {
		return nil, err
	}

	loadBalancerName := cloudprovider.GetLoadBalancerName(apiService)
	loadBalancerIP := apiService.Spec.LoadBalancerIP
	ports := apiService.Spec.Ports
	portStr := []string{}
	for _, p := range apiService.Spec.Ports {
		portStr = append(portStr, fmt.Sprintf("%s/%d", p.Protocol, p.Port))
	}

	affinityType := apiService.Spec.SessionAffinity

	serviceName := types.NamespacedName{Namespace: apiService.Namespace, Name: apiService.Name}
	glog.V(2).Infof("EnsureLoadBalancer(%v, %v, %v, %v, %v, %v, %v)", loadBalancerName, gce.region, loadBalancerIP, portStr, hostNames, serviceName, apiService.Annotations)

	// Check if the forwarding rule exists, and if so, what its IP is.
	fwdRuleExists, fwdRuleNeedsUpdate, fwdRuleIP, err := gce.forwardingRuleNeedsUpdate(loadBalancerName, gce.region, loadBalancerIP, ports)
	if err != nil {
		return nil, err
	}
	if !fwdRuleExists {
		glog.Infof("Forwarding rule %v for Service %v/%v doesn't exist", loadBalancerName, apiService.Namespace, apiService.Name)
	}

	// Make sure we know which IP address will be used and have properly reserved
	// it as static before moving forward with the rest of our operations.
	//
	// We use static IP addresses when updating a load balancer to ensure that we
	// can replace the load balancer's other components without changing the
	// address its service is reachable on. We do it this way rather than always
	// keeping the static IP around even though this is more complicated because
	// it makes it less likely that we'll run into quota issues. Only 7 static
	// IP addresses are allowed per region by default.
	//
	// We could let an IP be allocated for us when the forwarding rule is created,
	// but we need the IP to set up the firewall rule, and we want to keep the
	// forwarding rule creation as the last thing that needs to be done in this
	// function in order to maintain the invariant that "if the forwarding rule
	// exists, the LB has been fully created".
	ipAddress := ""

	// Through this process we try to keep track of whether it is safe to
	// release the IP that was allocated.  If the user specifically asked for
	// an IP, we assume they are managing it themselves.  Otherwise, we will
	// release the IP in case of early-terminating failure or upon successful
	// creating of the LB.
	// TODO(#36535): boil this logic down into a set of component functions
	// and key the flag values off of errors returned.
	isUserOwnedIP := false // if this is set, we never release the IP
	isSafeToReleaseIP := false
	defer func() {
		if isUserOwnedIP {
			return
		}
		if isSafeToReleaseIP {
			if err = gce.deleteStaticIP(loadBalancerName, gce.region); err != nil {
				glog.Errorf("failed to release static IP %s for load balancer (%v(%v), %v): %v", ipAddress, loadBalancerName, serviceName, gce.region, err)
			}
			glog.V(2).Infof("EnsureLoadBalancer(%v(%v)): released static IP %s", loadBalancerName, serviceName, ipAddress)
		} else {
			glog.Warningf("orphaning static IP %s during update of load balancer (%v(%v), %v): %v", ipAddress, loadBalancerName, serviceName, gce.region, err)
		}
	}()

	if loadBalancerIP != "" {
		// If a specific IP address has been requested, we have to respect the
		// user's request and use that IP. If the forwarding rule was already using
		// a different IP, it will be harmlessly abandoned because it was only an
		// ephemeral IP (or it was a different static IP owned by the user, in which
		// case we shouldn't delete it anyway).
		if isStatic, addrErr := gce.projectOwnsStaticIP(loadBalancerName, gce.region, loadBalancerIP); err != nil {
			return nil, fmt.Errorf("failed to test if this GCE project owns the static IP %s: %v", loadBalancerIP, addrErr)
		} else if isStatic {
			// The requested IP is a static IP, owned and managed by the user.
			isUserOwnedIP = true
			isSafeToReleaseIP = false
			ipAddress = loadBalancerIP
			glog.V(4).Infof("EnsureLoadBalancer(%v(%v)): using user-provided static IP %s", loadBalancerName, serviceName, ipAddress)
		} else if loadBalancerIP == fwdRuleIP {
			// The requested IP is not a static IP, but is currently assigned
			// to this forwarding rule, so we can keep it.
			isUserOwnedIP = false
			isSafeToReleaseIP = true
			ipAddress, _, err = gce.ensureStaticIP(loadBalancerName, serviceName.String(), gce.region, fwdRuleIP)
			if err != nil {
				return nil, fmt.Errorf("failed to ensure static IP %s: %v", fwdRuleIP, err)
			}
			glog.V(4).Infof("EnsureLoadBalancer(%v(%v)): using user-provided non-static IP %s", loadBalancerName, serviceName, ipAddress)
		} else {
			// The requested IP is not static and it is not assigned to the
			// current forwarding rule.  It might be attached to a different
			// rule or it might not be part of this project at all.  Either
			// way, we can't use it.
			return nil, fmt.Errorf("requested ip %s is neither static nor assigned to LB %s(%v): %v", loadBalancerIP, loadBalancerName, serviceName, err)
		}
	} else {
		// The user did not request a specific IP.
		isUserOwnedIP = false

		// This will either allocate a new static IP if the forwarding rule didn't
		// already have an IP, or it will promote the forwarding rule's current
		// IP from ephemeral to static, or it will just get the IP if it is
		// already static.
		existed := false
		ipAddress, existed, err = gce.ensureStaticIP(loadBalancerName, serviceName.String(), gce.region, fwdRuleIP)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure static IP %s: %v", fwdRuleIP, err)
		}
		if existed {
			// If the IP was not specifically requested by the user, but it
			// already existed, it seems to be a failed update cycle.  We can
			// use this IP and try to run through the process again, but we
			// should not release the IP unless it is explicitly flagged as OK.
			isSafeToReleaseIP = false
			glog.V(4).Infof("EnsureLoadBalancer(%v(%v)): adopting static IP %s", loadBalancerName, serviceName, ipAddress)
		} else {
			// For total clarity.  The IP did not pre-exist and the user did
			// not ask for a particular one, so we can release the IP in case
			// of failure or success.
			isSafeToReleaseIP = true
			glog.V(4).Infof("EnsureLoadBalancer(%v(%v)): allocated static IP %s", loadBalancerName, serviceName, ipAddress)
		}
	}

	// Deal with the firewall next. The reason we do this here rather than last
	// is because the forwarding rule is used as the indicator that the load
	// balancer is fully created - it's what getLoadBalancer checks for.
	// Check if user specified the allow source range
	sourceRanges, err := apiservice.GetLoadBalancerSourceRanges(apiService)
	if err != nil {
		return nil, err
	}

	firewallExists, firewallNeedsUpdate, err := gce.firewallNeedsUpdate(loadBalancerName, serviceName.String(), gce.region, ipAddress, ports, sourceRanges)
	if err != nil {
		return nil, err
	}

	if firewallNeedsUpdate {
		desc := makeFirewallDescription(serviceName.String(), ipAddress)
		// Unlike forwarding rules and target pools, firewalls can be updated
		// without needing to be deleted and recreated.
		if firewallExists {
			glog.Infof("EnsureLoadBalancer(%v(%v)): updating firewall", loadBalancerName, serviceName)
			if err = gce.updateFirewall(loadBalancerName, gce.region, desc, sourceRanges, ports, hosts); err != nil {
				return nil, err
			}
			glog.Infof("EnsureLoadBalancer(%v(%v)): updated firewall", loadBalancerName, serviceName)
		} else {
			glog.Infof("EnsureLoadBalancer(%v(%v)): creating firewall", loadBalancerName, serviceName)
			if err = gce.createFirewall(loadBalancerName, gce.region, desc, sourceRanges, ports, hosts); err != nil {
				return nil, err
			}
			glog.Infof("EnsureLoadBalancer(%v(%v)): created firewall", loadBalancerName, serviceName)
		}
	}

	tpExists, tpNeedsUpdate, err := gce.targetPoolNeedsUpdate(loadBalancerName, gce.region, affinityType)
	if err != nil {
		return nil, err
	}
	if !tpExists {
		glog.Infof("Target pool %v for Service %v/%v doesn't exist", loadBalancerName, apiService.Namespace, apiService.Name)
	}

	// Ensure health checks are created for this target pool to pass to createTargetPool for health check links
	// Alternately, if the annotation on the service was removed, we need to recreate the target pool without
	// health checks. This needs to be prior to the forwarding rule deletion below otherwise it is not possible
	// to delete just the target pool or http health checks later.
	var hcToCreate *compute.HttpHealthCheck
	hcExisting, err := gce.GetHTTPHealthCheck(loadBalancerName)
	if err != nil && !isHTTPErrorCode(err, http.StatusNotFound) {
		return nil, fmt.Errorf("Error checking HTTP health check %s: %v", loadBalancerName, err)
	}
	if path, healthCheckNodePort := apiservice.GetServiceHealthCheckPathPort(apiService); path != "" {
		glog.V(4).Infof("service %v (%v) needs health checks on :%d%s)", apiService.Name, loadBalancerName, healthCheckNodePort, path)
		if err != nil {
			// This logic exists to detect a transition for a pre-existing service and turn on
			// the tpNeedsUpdate flag to delete/recreate fwdrule/tpool adding the health check
			// to the target pool.
			glog.V(2).Infof("Annotation external-traffic=OnlyLocal added to new or pre-existing service")
			tpNeedsUpdate = true
		}
		hcToCreate, err = gce.ensureHTTPHealthCheck(loadBalancerName, path, healthCheckNodePort)
		if err != nil {
			return nil, fmt.Errorf("Failed to ensure health check for localized service %v on node port %v: %v", loadBalancerName, healthCheckNodePort, err)
		}
	} else {
		glog.V(4).Infof("service %v does not need health checks", apiService.Name)
		if err == nil {
			glog.V(2).Infof("Deleting stale health checks for service %v LB %v", apiService.Name, loadBalancerName)
			tpNeedsUpdate = true
		}
	}
	// Now we get to some slightly more interesting logic.
	// First, neither target pools nor forwarding rules can be updated in place -
	// they have to be deleted and recreated.
	// Second, forwarding rules are layered on top of target pools in that you
	// can't delete a target pool that's currently in use by a forwarding rule.
	// Thus, we have to tear down the forwarding rule if either it or the target
	// pool needs to be updated.
	if fwdRuleExists && (fwdRuleNeedsUpdate || tpNeedsUpdate) {
		// Begin critical section. If we have to delete the forwarding rule,
		// and something should fail before we recreate it, don't release the
		// IP.  That way we can come back to it later.
		isSafeToReleaseIP = false
		if err := gce.deleteForwardingRule(loadBalancerName, gce.region); err != nil {
			return nil, fmt.Errorf("failed to delete existing forwarding rule %s for load balancer update: %v", loadBalancerName, err)
		}
		glog.Infof("EnsureLoadBalancer(%v(%v)): deleted forwarding rule", loadBalancerName, serviceName)
	}
	if tpExists && tpNeedsUpdate {
		// Generate the list of health checks for this target pool to pass to deleteTargetPool
		if path, _ := apiservice.GetServiceHealthCheckPathPort(apiService); path != "" {
			var err error
			hcExisting, err = gce.GetHTTPHealthCheck(loadBalancerName)
			if err != nil && !isHTTPErrorCode(err, http.StatusNotFound) {
				glog.Infof("Failed to retrieve health check %v:%v", loadBalancerName, err)
			}
		}

		// Pass healthchecks to deleteTargetPool to cleanup health checks prior to cleaning up the target pool itself.
		if err := gce.deleteTargetPool(loadBalancerName, gce.region, hcExisting); err != nil {
			return nil, fmt.Errorf("failed to delete existing target pool %s for load balancer update: %v", loadBalancerName, err)
		}
		glog.Infof("EnsureLoadBalancer(%v(%v)): deleted target pool", loadBalancerName, serviceName)
	}

	// Once we've deleted the resources (if necessary), build them back up (or for
	// the first time if they're new).
	if tpNeedsUpdate {
		createInstances := hosts
		if len(hosts) > maxTargetPoolCreateInstances {
			createInstances = createInstances[:maxTargetPoolCreateInstances]
		}
		// Pass healthchecks to createTargetPool which needs them as health check links in the target pool
		if err := gce.createTargetPool(loadBalancerName, serviceName.String(), gce.region, createInstances, affinityType, hcToCreate); err != nil {
			return nil, fmt.Errorf("failed to create target pool %s: %v", loadBalancerName, err)
		}
		if hcToCreate != nil {
			glog.Infof("EnsureLoadBalancer(%v(%v)): created health checks for target pool", loadBalancerName, serviceName)
		}
		if len(hosts) <= maxTargetPoolCreateInstances {
			glog.Infof("EnsureLoadBalancer(%v(%v)): created target pool", loadBalancerName, serviceName)
		} else {
			glog.Infof("EnsureLoadBalancer(%v(%v)): created initial target pool (now updating with %d hosts)", loadBalancerName, serviceName, len(hosts)-maxTargetPoolCreateInstances)

			created := sets.NewString()
			for _, host := range createInstances {
				created.Insert(host.makeComparableHostPath())
			}
			if err := gce.updateTargetPool(loadBalancerName, created, hosts); err != nil {
				return nil, fmt.Errorf("failed to update target pool %s: %v", loadBalancerName, err)
			}
			glog.Infof("EnsureLoadBalancer(%v(%v)): updated target pool (with %d hosts)", loadBalancerName, serviceName, len(hosts)-maxTargetPoolCreateInstances)
		}
	}
	if tpNeedsUpdate || fwdRuleNeedsUpdate {
		glog.Infof("EnsureLoadBalancer(%v(%v)): creating forwarding rule, IP %s", loadBalancerName, serviceName, ipAddress)
		if err := gce.createForwardingRule(loadBalancerName, serviceName.String(), gce.region, ipAddress, ports); err != nil {
			return nil, fmt.Errorf("failed to create forwarding rule %s: %v", loadBalancerName, err)
		}
		// End critical section.  It is safe to release the static IP (which
		// just demotes it to ephemeral) now that it is attached.  In the case
		// of a user-requested IP, the "is user-owned" flag will be set,
		// preventing it from actually being released.
		isSafeToReleaseIP = true
		glog.Infof("EnsureLoadBalancer(%v(%v)): created forwarding rule, IP %s", loadBalancerName, serviceName, ipAddress)
	}

	status := &v1.LoadBalancerStatus{}
	status.Ingress = []v1.LoadBalancerIngress{{IP: ipAddress}}

	return status, nil
}

func makeHealthCheckDescription(serviceName string) string {
	return fmt.Sprintf(`{"kubernetes.io/service-name":"%s"}`, serviceName)
}

func (gce *Cloud) ensureHTTPHealthCheck(name, path string, port int32) (hc *compute.HttpHealthCheck, err error) {
	newHC := &compute.HttpHealthCheck{
		Name:               name,
		Port:               int64(port),
		RequestPath:        path,
		Host:               "",
		Description:        makeHealthCheckDescription(name),
		CheckIntervalSec:   gceHcCheckIntervalSeconds,
		TimeoutSec:         gceHcTimeoutSeconds,
		HealthyThreshold:   gceHcHealthyThreshold,
		UnhealthyThreshold: gceHcUnhealthyThreshold,
	}

	hc, err = gce.GetHTTPHealthCheck(name)
	if hc == nil || err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Did not find health check %v, creating port %v path %v", name, port, path)
		if err = gce.CreateHTTPHealthCheck(newHC); err != nil {
			return nil, err
		}
		hc, err = gce.GetHTTPHealthCheck(name)
		if err != nil {
			glog.Errorf("Failed to get http health check %v", err)
			return nil, err
		}
		glog.Infof("Created HTTP health check %v healthCheckNodePort: %d", name, port)
		return hc, nil
	}
	// Validate health check fields
	glog.V(4).Infof("Checking http health check params %s", name)
	drift := hc.Port != int64(port) || hc.RequestPath != path || hc.Description != makeHealthCheckDescription(name)
	drift = drift || hc.CheckIntervalSec != gceHcCheckIntervalSeconds || hc.TimeoutSec != gceHcTimeoutSeconds
	drift = drift || hc.UnhealthyThreshold != gceHcUnhealthyThreshold || hc.HealthyThreshold != gceHcHealthyThreshold
	if drift {
		glog.Warningf("Health check %v exists but parameters have drifted - updating...", name)
		if err := gce.UpdateHTTPHealthCheck(newHC); err != nil {
			glog.Warningf("Failed to reconcile http health check %v parameters", name)
			return nil, err
		}
		glog.V(4).Infof("Corrected health check %v parameters successful", name)
	}
	return hc, nil
}

// Passing nil for requested IP is perfectly fine - it just means that no specific
// IP is being requested.
// Returns whether the forwarding rule exists, whether it needs to be updated,
// what its IP address is (if it exists), and any error we encountered.
func (gce *Cloud) forwardingRuleNeedsUpdate(name, region string, loadBalancerIP string, ports []v1.ServicePort) (exists bool, needsUpdate bool, ipAddress string, err error) {
	fwd, err := gce.service.ForwardingRules.Get(gce.projectID, region, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return false, true, "", nil
		}
		// Err on the side of caution in case of errors. Caller should notice the error and retry.
		// We never want to end up recreating resources because gce api flaked.
		return true, false, "", fmt.Errorf("error getting load balancer's forwarding rule: %v", err)
	}
	// If the user asks for a specific static ip through the Service spec,
	// check that we're actually using it.
	// TODO: we report loadbalancer IP through status, so we want to verify if
	// that matches the forwarding rule as well.
	if loadBalancerIP != "" && loadBalancerIP != fwd.IPAddress {
		glog.Infof("LoadBalancer ip for forwarding rule %v was expected to be %v, but was actually %v", fwd.Name, fwd.IPAddress, loadBalancerIP)
		return true, true, fwd.IPAddress, nil
	}
	portRange, err := loadBalancerPortRange(ports)
	if err != nil {
		// Err on the side of caution in case of errors. Caller should notice the error and retry.
		// We never want to end up recreating resources because gce api flaked.
		return true, false, "", err
	}
	if portRange != fwd.PortRange {
		glog.Infof("LoadBalancer port range for forwarding rule %v was expected to be %v, but was actually %v", fwd.Name, fwd.PortRange, portRange)
		return true, true, fwd.IPAddress, nil
	}
	// The service controller verified all the protocols match on the ports, just check the first one
	if string(ports[0].Protocol) != fwd.IPProtocol {
		glog.Infof("LoadBalancer protocol for forwarding rule %v was expected to be %v, but was actually %v", fwd.Name, fwd.IPProtocol, string(ports[0].Protocol))
		return true, true, fwd.IPAddress, nil
	}

	return true, false, fwd.IPAddress, nil
}

func loadBalancerPortRange(ports []v1.ServicePort) (string, error) {
	if len(ports) == 0 {
		return "", fmt.Errorf("no ports specified for GCE load balancer")
	}

	// The service controller verified all the protocols match on the ports, just check and use the first one
	if ports[0].Protocol != v1.ProtocolTCP && ports[0].Protocol != v1.ProtocolUDP {
		return "", fmt.Errorf("Invalid protocol %s, only TCP and UDP are supported", string(ports[0].Protocol))
	}

	minPort := int32(65536)
	maxPort := int32(0)
	for i := range ports {
		if ports[i].Port < minPort {
			minPort = ports[i].Port
		}
		if ports[i].Port > maxPort {
			maxPort = ports[i].Port
		}
	}
	return fmt.Sprintf("%d-%d", minPort, maxPort), nil
}

// Doesn't check whether the hosts have changed, since host updating is handled
// separately.
func (gce *Cloud) targetPoolNeedsUpdate(name, region string, affinityType v1.ServiceAffinity) (exists bool, needsUpdate bool, err error) {
	tp, err := gce.service.TargetPools.Get(gce.projectID, region, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return false, true, nil
		}
		// Err on the side of caution in case of errors. Caller should notice the error and retry.
		// We never want to end up recreating resources because gce api flaked.
		return true, false, fmt.Errorf("error getting load balancer's target pool: %v", err)
	}
	// TODO: If the user modifies their Service's session affinity, it *should*
	// reflect in the associated target pool. However, currently not setting the
	// session affinity on a target pool defaults it to the empty string while
	// not setting in on a Service defaults it to None. There is a lack of
	// documentation around the default setting for the target pool, so if we
	// find it's the undocumented empty string, don't blindly recreate the
	// target pool (which results in downtime). Fix this when we have formally
	// defined the defaults on either side.
	if tp.SessionAffinity != "" && translateAffinityType(affinityType) != tp.SessionAffinity {
		glog.Infof("LoadBalancer target pool %v changed affinity from %v to %v", name, tp.SessionAffinity, affinityType)
		return true, true, nil
	}
	return true, false, nil
}

// translate from what K8s supports to what the cloud provider supports for session affinity.
func translateAffinityType(affinityType v1.ServiceAffinity) string {
	switch affinityType {
	case v1.ServiceAffinityClientIP:
		return gceAffinityTypeClientIP
	case v1.ServiceAffinityNone:
		return gceAffinityTypeNone
	default:
		glog.Errorf("Unexpected affinity type: %v", affinityType)
		return gceAffinityTypeNone
	}
}

func (gce *Cloud) firewallNeedsUpdate(name, serviceName, region, ipAddress string, ports []v1.ServicePort, sourceRanges netsets.IPNet) (exists bool, needsUpdate bool, err error) {
	fw, err := gce.service.Firewalls.Get(gce.projectID, makeFirewallName(name)).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return false, true, nil
		}
		return false, false, fmt.Errorf("error getting load balancer's target pool: %v", err)
	}
	if fw.Description != makeFirewallDescription(serviceName, ipAddress) {
		return true, true, nil
	}
	if len(fw.Allowed) != 1 || (fw.Allowed[0].IPProtocol != "tcp" && fw.Allowed[0].IPProtocol != "udp") {
		return true, true, nil
	}
	// Make sure the allowed ports match.
	allowedPorts := make([]string, len(ports))
	for ix := range ports {
		allowedPorts[ix] = strconv.Itoa(int(ports[ix].Port))
	}
	if !slicesEqual(allowedPorts, fw.Allowed[0].Ports) {
		return true, true, nil
	}
	// The service controller already verified that the protocol matches on all ports, no need to check.

	actualSourceRanges, err := netsets.ParseIPNets(fw.SourceRanges...)
	if err != nil {
		// This really shouldn't happen... GCE has returned something unexpected
		glog.Warningf("Error parsing firewall SourceRanges: %v", fw.SourceRanges)
		// We don't return the error, because we can hopefully recover from this by reconfiguring the firewall
		return true, true, nil
	}

	if !sourceRanges.Equal(actualSourceRanges) {
		return true, true, nil
	}
	return true, false, nil
}

func makeFirewallName(name string) string {
	return fmt.Sprintf("k8s-fw-%s", name)
}

func makeFirewallDescription(serviceName, ipAddress string) string {
	return fmt.Sprintf(`{"kubernetes.io/service-name":"%s", "kubernetes.io/service-ip":"%s"}`,
		serviceName, ipAddress)
}

func slicesEqual(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (gce *Cloud) createForwardingRule(name, serviceName, region, ipAddress string, ports []v1.ServicePort) error {
	portRange, err := loadBalancerPortRange(ports)
	if err != nil {
		return err
	}
	req := &compute.ForwardingRule{
		Name:        name,
		Description: fmt.Sprintf(`{"kubernetes.io/service-name":"%s"}`, serviceName),
		IPAddress:   ipAddress,
		IPProtocol:  string(ports[0].Protocol),
		PortRange:   portRange,
		Target:      gce.targetPoolURL(name, region),
	}

	op, err := gce.service.ForwardingRules.Insert(gce.projectID, region, req).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForRegionOp(op, region)
		if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
			return err
		}
	}
	return nil
}

func (gce *Cloud) createTargetPool(name, serviceName, region string, hosts []*gceInstance, affinityType v1.ServiceAffinity, hc *compute.HttpHealthCheck) error {
	var instances []string
	for _, host := range hosts {
		instances = append(instances, makeHostURL(gce.projectID, host.Zone, host.Name))
	}
	// health check management is coupled with targetPools to prevent leaks. A
	// target pool is the only thing that requires a health check, so we delete
	// associated checks on teardown, and ensure checks on setup.
	hcLinks := []string{}
	if hc != nil {
		var err error
		if hc, err = gce.ensureHTTPHealthCheck(name, hc.RequestPath, int32(hc.Port)); err != nil || hc == nil {
			return fmt.Errorf("Failed to ensure health check for %v port %d path %v: %v", name, hc.Port, hc.RequestPath, err)
		}
		hcLinks = append(hcLinks, hc.SelfLink)
	}
	glog.Infof("Creating targetpool %v with %d healthchecks", name, len(hcLinks))
	pool := &compute.TargetPool{
		Name:            name,
		Description:     fmt.Sprintf(`{"kubernetes.io/service-name":"%s"}`, serviceName),
		Instances:       instances,
		SessionAffinity: translateAffinityType(affinityType),
		HealthChecks:    hcLinks,
	}
	op, err := gce.service.TargetPools.Insert(gce.projectID, region, pool).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForRegionOp(op, region)
		if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
			return err
		}
	}
	return nil
}

func (gce *Cloud) createFirewall(name, region, desc string, sourceRanges netsets.IPNet, ports []v1.ServicePort, hosts []*gceInstance) error {
	firewall, err := gce.firewallObject(name, region, desc, sourceRanges, ports, hosts)
	if err != nil {
		return err
	}
	op, err := gce.service.Firewalls.Insert(gce.projectID, firewall).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForGlobalOp(op)
		if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
			return err
		}
	}
	return nil
}

func (gce *Cloud) updateFirewall(name, region, desc string, sourceRanges netsets.IPNet, ports []v1.ServicePort, hosts []*gceInstance) error {
	firewall, err := gce.firewallObject(name, region, desc, sourceRanges, ports, hosts)
	if err != nil {
		return err
	}
	op, err := gce.service.Firewalls.Update(gce.projectID, makeFirewallName(name), firewall).Do()
	if err != nil && !isHTTPErrorCode(err, http.StatusConflict) {
		return err
	}
	if op != nil {
		err = gce.waitForGlobalOp(op)
		if err != nil {
			return err
		}
	}
	return nil
}

func (gce *Cloud) firewallObject(name, region, desc string, sourceRanges netsets.IPNet, ports []v1.ServicePort, hosts []*gceInstance) (*compute.Firewall, error) {
	allowedPorts := make([]string, len(ports))
	for ix := range ports {
		allowedPorts[ix] = strconv.Itoa(int(ports[ix].Port))
	}
	// If the node tags to be used for this cluster have been predefined in the
	// provider config, just use them. Otherwise, invoke computeHostTags method to get the tags.
	hostTags := gce.nodeTags
	if len(hostTags) == 0 {
		var err error
		if hostTags, err = gce.computeHostTags(hosts); err != nil {
			return nil, fmt.Errorf("No node tags supplied and also failed to parse the given lists of hosts for tags. Abort creating firewall rule")
		}
	}

	firewall := &compute.Firewall{
		Name:         makeFirewallName(name),
		Description:  desc,
		Network:      gce.networkURL,
		SourceRanges: sourceRanges.StringSlice(),
		TargetTags:   hostTags,
		Allowed: []*compute.FirewallAllowed{
			{
				// TODO: Make this more generic. Currently this method is only
				// used to create firewall rules for loadbalancers, which have
				// exactly one protocol, so we can never end up with a list of
				// mixed TCP and UDP ports. It should be possible to use a
				// single firewall rule for both a TCP and UDP lb.
				IPProtocol: strings.ToLower(string(ports[0].Protocol)),
				Ports:      allowedPorts,
			},
		},
	}
	return firewall, nil
}

// ComputeHostTags grabs all tags from all instances being added to the pool.
// * The longest tag that is a prefix of the instance name is used
// * If any instance has no matching prefix tag, return error
// Invoking this method to get host tags is risky since it depends on the format
// of the host names in the cluster. Only use it as a fallback if gce.nodeTags
// is unspecified
func (gce *Cloud) computeHostTags(hosts []*gceInstance) ([]string, error) {
	// TODO: We could store the tags in gceInstance, so we could have already fetched it
	hostNamesByZone := make(map[string]map[string]bool) // map of zones -> map of names -> bool (for easy lookup)
	nodeInstancePrefix := gce.nodeInstancePrefix
	for _, host := range hosts {
		if !strings.HasPrefix(host.Name, gce.nodeInstancePrefix) {
			glog.Warningf("instance '%v' does not conform to prefix '%s', ignoring filter", host, gce.nodeInstancePrefix)
			nodeInstancePrefix = ""
		}

		z, ok := hostNamesByZone[host.Zone]
		if !ok {
			z = make(map[string]bool)
			hostNamesByZone[host.Zone] = z
		}
		z[host.Name] = true
	}

	tags := sets.NewString()

	for zone, hostNames := range hostNamesByZone {
		pageToken := ""
		page := 0
		for ; page == 0 || (pageToken != "" && page < maxPages); page++ {
			listCall := gce.service.Instances.List(gce.projectID, zone)

			if nodeInstancePrefix != "" {
				// Add the filter for hosts
				listCall = listCall.Filter("name eq " + nodeInstancePrefix + ".*")
			}

			// Add the fields we want
			// TODO(zmerlynn): Internal bug 29524655
			// listCall = listCall.Fields("items(name,tags)")

			if pageToken != "" {
				listCall = listCall.PageToken(pageToken)
			}

			res, err := listCall.Do()
			if err != nil {
				return nil, err
			}
			pageToken = res.NextPageToken
			for _, instance := range res.Items {
				if !hostNames[instance.Name] {
					continue
				}

				longestTag := ""
				for _, tag := range instance.Tags.Items {
					if strings.HasPrefix(instance.Name, tag) && len(tag) > len(longestTag) {
						longestTag = tag
					}
				}
				if len(longestTag) > 0 {
					tags.Insert(longestTag)
				} else {
					return nil, fmt.Errorf("Could not find any tag that is a prefix of instance name for instance %s", instance.Name)
				}
			}
		}
		if page >= maxPages {
			glog.Errorf("computeHostTags exceeded maxPages=%d for Instances.List: truncating.", maxPages)
		}
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("No instances found")
	}
	return tags.List(), nil
}

func (gce *Cloud) projectOwnsStaticIP(name, region string, ipAddress string) (bool, error) {
	pageToken := ""
	page := 0
	for ; page == 0 || (pageToken != "" && page < maxPages); page++ {
		listCall := gce.service.Addresses.List(gce.projectID, region)
		if pageToken != "" {
			listCall = listCall.PageToken(pageToken)
		}
		addresses, err := listCall.Do()
		if err != nil {
			return false, fmt.Errorf("failed to list gce IP addresses: %v", err)
		}
		pageToken = addresses.NextPageToken
		for _, addr := range addresses.Items {
			if addr.Address == ipAddress {
				// This project does own the address, so return success.
				return true, nil
			}
		}
	}
	if page >= maxPages {
		glog.Errorf("projectOwnsStaticIP exceeded maxPages=%d for Addresses.List; truncating.", maxPages)
	}
	return false, nil
}

func (gce *Cloud) ensureStaticIP(name, serviceName, region, existingIP string) (ipAddress string, created bool, err error) {
	// If the address doesn't exist, this will create it.
	// If the existingIP exists but is ephemeral, this will promote it to static.
	// If the address already exists, this will harmlessly return a StatusConflict
	// and we'll grab the IP before returning.
	existed := false
	addressObj := &compute.Address{
		Name:        name,
		Description: fmt.Sprintf(`{"kubernetes.io/service-name":"%s"}`, serviceName),
	}
	if existingIP != "" {
		addressObj.Address = existingIP
	}
	op, err := gce.service.Addresses.Insert(gce.projectID, region, addressObj).Do()
	if err != nil {
		if !isHTTPErrorCode(err, http.StatusConflict) {
			return "", false, fmt.Errorf("error creating gce static IP address: %v", err)
		}
		// StatusConflict == the IP exists already.
		existed = true
	}
	if op != nil {
		err = gce.waitForRegionOp(op, region)
		if err != nil {
			if !isHTTPErrorCode(err, http.StatusConflict) {
				return "", false, fmt.Errorf("error waiting for gce static IP address to be created: %v", err)
			}
			// StatusConflict == the IP exists already.
			existed = true
		}
	}

	// We have to get the address to know which IP was allocated for us.
	address, err := gce.service.Addresses.Get(gce.projectID, region, name).Do()
	if err != nil {
		return "", false, fmt.Errorf("error re-getting gce static IP address: %v", err)
	}
	return address.Address, existed, nil
}

// UpdateLoadBalancer is an implementation of LoadBalancer.UpdateLoadBalancer.
func (gce *Cloud) UpdateLoadBalancer(clusterName string, service *v1.Service, nodes []*v1.Node) error {
	hosts, err := gce.getInstancesByNames(nodeNames(nodes))
	if err != nil {
		return err
	}

	loadBalancerName := cloudprovider.GetLoadBalancerName(service)
	pool, err := gce.service.TargetPools.Get(gce.projectID, gce.region, loadBalancerName).Do()
	if err != nil {
		return err
	}
	existing := sets.NewString()
	for _, instance := range pool.Instances {
		existing.Insert(hostURLToComparablePath(instance))
	}

	return gce.updateTargetPool(loadBalancerName, existing, hosts)
}

func (gce *Cloud) updateTargetPool(loadBalancerName string, existing sets.String, hosts []*gceInstance) error {
	var toAdd []*compute.InstanceReference
	var toRemove []*compute.InstanceReference
	for _, host := range hosts {
		link := host.makeComparableHostPath()
		if !existing.Has(link) {
			toAdd = append(toAdd, &compute.InstanceReference{Instance: link})
		}
		existing.Delete(link)
	}
	for link := range existing {
		toRemove = append(toRemove, &compute.InstanceReference{Instance: link})
	}

	if len(toAdd) > 0 {
		add := &compute.TargetPoolsAddInstanceRequest{Instances: toAdd}
		op, err := gce.service.TargetPools.AddInstance(gce.projectID, gce.region, loadBalancerName, add).Do()
		if err != nil {
			return err
		}
		if err := gce.waitForRegionOp(op, gce.region); err != nil {
			return err
		}
	}

	if len(toRemove) > 0 {
		rm := &compute.TargetPoolsRemoveInstanceRequest{Instances: toRemove}
		op, err := gce.service.TargetPools.RemoveInstance(gce.projectID, gce.region, loadBalancerName, rm).Do()
		if err != nil {
			return err
		}
		if err := gce.waitForRegionOp(op, gce.region); err != nil {
			return err
		}
	}

	// Try to verify that the correct number of nodes are now in the target pool.
	// We've been bitten by a bug here before (#11327) where all nodes were
	// accidentally removed and want to make similar problems easier to notice.
	updatedPool, err := gce.service.TargetPools.Get(gce.projectID, gce.region, loadBalancerName).Do()
	if err != nil {
		return err
	}
	if len(updatedPool.Instances) != len(hosts) {
		glog.Errorf("Unexpected number of instances (%d) in target pool %s after updating (expected %d). Instances in updated pool: %s",
			len(updatedPool.Instances), loadBalancerName, len(hosts), strings.Join(updatedPool.Instances, ","))
		return fmt.Errorf("Unexpected number of instances (%d) in target pool %s after update (expected %d)", len(updatedPool.Instances), loadBalancerName, len(hosts))
	}
	return nil
}

// EnsureLoadBalancerDeleted is an implementation of LoadBalancer.EnsureLoadBalancerDeleted.
func (gce *Cloud) EnsureLoadBalancerDeleted(clusterName string, service *v1.Service) error {
	loadBalancerName := cloudprovider.GetLoadBalancerName(service)
	glog.V(2).Infof("EnsureLoadBalancerDeleted(%v, %v, %v, %v, %v)", clusterName, service.Namespace, service.Name, loadBalancerName,
		gce.region)

	var hc *compute.HttpHealthCheck
	if path, _ := apiservice.GetServiceHealthCheckPathPort(service); path != "" {
		var err error
		hc, err = gce.GetHTTPHealthCheck(loadBalancerName)
		if err != nil && !isHTTPErrorCode(err, http.StatusNotFound) {
			glog.Infof("Failed to retrieve health check %v:%v", loadBalancerName, err)
			return err
		}
	}

	errs := utilerrors.AggregateGoroutines(
		func() error { return gce.deleteFirewall(loadBalancerName, gce.region) },
		// Even though we don't hold on to static IPs for load balancers, it's
		// possible that EnsureLoadBalancer left one around in a failed
		// creation/update attempt, so make sure we clean it up here just in case.
		func() error { return gce.deleteStaticIP(loadBalancerName, gce.region) },
		func() error {
			// The forwarding rule must be deleted before either the target pool can,
			// unfortunately, so we have to do these two serially.
			if err := gce.deleteForwardingRule(loadBalancerName, gce.region); err != nil {
				return err
			}
			if err := gce.deleteTargetPool(loadBalancerName, gce.region, hc); err != nil {
				return err
			}
			return nil
		},
	)
	if errs != nil {
		return utilerrors.Flatten(errs)
	}
	return nil
}

func (gce *Cloud) deleteForwardingRule(name, region string) error {
	op, err := gce.service.ForwardingRules.Delete(gce.projectID, region, name).Do()
	if err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Forwarding rule %s already deleted. Continuing to delete other resources.", name)
	} else if err != nil {
		glog.Warningf("Failed to delete forwarding rule %s: got error %s.", name, err.Error())
		return err
	} else {
		if err := gce.waitForRegionOp(op, region); err != nil {
			glog.Warningf("Failed waiting for forwarding rule %s to be deleted: got error %s.", name, err.Error())
			return err
		}
	}
	return nil
}

// DeleteForwardingRule deletes the forwarding rule
func (gce *Cloud) DeleteForwardingRule(name string) error {
	region, err := GetGCERegion(gce.localZone)
	if err != nil {
		return err
	}
	return gce.deleteForwardingRule(name, region)
}

// DeleteTargetPool deletes the given target pool.
func (gce *Cloud) DeleteTargetPool(name string, hc *compute.HttpHealthCheck) error {
	region, err := GetGCERegion(gce.localZone)
	if err != nil {
		return err
	}
	return gce.deleteTargetPool(name, region, hc)
}

func (gce *Cloud) deleteTargetPool(name, region string, hc *compute.HttpHealthCheck) error {
	op, err := gce.service.TargetPools.Delete(gce.projectID, region, name).Do()
	if err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Target pool %s already deleted. Continuing to delete other resources.", name)
	} else if err != nil {
		glog.Warningf("Failed to delete target pool %s, got error %s.", name, err.Error())
		return err
	} else {
		if err := gce.waitForRegionOp(op, region); err != nil {
			glog.Warningf("Failed waiting for target pool %s to be deleted: got error %s.", name, err.Error())
			return err
		}
	}
	// Deletion of health checks is allowed only after the TargetPool reference is deleted
	if hc != nil {
		glog.Infof("Deleting health check %v", hc.Name)
		if err := gce.DeleteHTTPHealthCheck(hc.Name); err != nil {
			glog.Warningf("Failed to delete health check %v: %v", hc, err)
			return err
		}
	} else {
		// This is a HC cleanup attempt to prevent stale HCs when errors are encountered
		// during HC deletion in a prior pass through EnsureLoadBalancer.
		// The HC name matches the load balancer name - normally this is expected to fail.
		if err := gce.DeleteHTTPHealthCheck(name); err == nil {
			// We only print a warning if this deletion actually succeeded (which
			// means there was indeed a stale health check with the LB name.
			glog.Warningf("Deleted stale http health check for LB: %s", name)
		}
	}
	return nil
}

func (gce *Cloud) deleteFirewall(name, region string) error {
	fwName := makeFirewallName(name)
	op, err := gce.service.Firewalls.Delete(gce.projectID, fwName).Do()
	if err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Firewall %s already deleted. Continuing to delete other resources.", name)
	} else if err != nil {
		glog.Warningf("Failed to delete firewall %s, got error %v", fwName, err)
		return err
	} else {
		if err := gce.waitForGlobalOp(op); err != nil {
			glog.Warningf("Failed waiting for Firewall %s to be deleted.  Got error: %v", fwName, err)
			return err
		}
	}
	return nil
}

func (gce *Cloud) deleteStaticIP(name, region string) error {
	op, err := gce.service.Addresses.Delete(gce.projectID, region, name).Do()
	if err != nil && isHTTPErrorCode(err, http.StatusNotFound) {
		glog.Infof("Static IP address %s is not reserved", name)
	} else if err != nil {
		glog.Warningf("Failed to delete static IP address %s, got error %v", name, err)
		return err
	} else {
		if err := gce.waitForRegionOp(op, region); err != nil {
			glog.Warningf("Failed waiting for address %s to be deleted, got error: %v", name, err)
			return err
		}
	}
	return nil
}

// Firewall management: These methods are just passthrough to the existing
// internal firewall creation methods used to manage TCPLoadBalancer.

// GetFirewall returns the Firewall by name.
func (gce *Cloud) GetFirewall(name string) (*compute.Firewall, error) {
	return gce.service.Firewalls.Get(gce.projectID, name).Do()
}

// CreateFirewall creates the given firewall rule.
func (gce *Cloud) CreateFirewall(name, desc string, sourceRanges netsets.IPNet, ports []int64, hostNames []string) error {
	region, err := GetGCERegion(gce.localZone)
	if err != nil {
		return err
	}
	// TODO: This completely breaks modularity in the cloudprovider but the methods
	// shared with the TCPLoadBalancer take v1.ServicePorts.
	svcPorts := []v1.ServicePort{}
	// TODO: Currently the only consumer of this method is the GCE L7
	// loadbalancer controller, which never needs a protocol other than TCP.
	// We should pipe through a mapping of port:protocol and default to TCP
	// if UDP ports are required. This means the method signature will change
	// forcing downstream clients to refactor interfaces.
	for _, p := range ports {
		svcPorts = append(svcPorts, v1.ServicePort{Port: int32(p), Protocol: v1.ProtocolTCP})
	}
	hosts, err := gce.getInstancesByNames(hostNames)
	if err != nil {
		return err
	}
	return gce.createFirewall(name, region, desc, sourceRanges, svcPorts, hosts)
}

// DeleteFirewall deletes the given firewall rule.
func (gce *Cloud) DeleteFirewall(name string) error {
	region, err := GetGCERegion(gce.localZone)
	if err != nil {
		return err
	}
	return gce.deleteFirewall(name, region)
}

// UpdateFirewall applies the given firewall rule as an update to an existing
// firewall rule with the same name.
func (gce *Cloud) UpdateFirewall(name, desc string, sourceRanges netsets.IPNet, ports []int64, hostNames []string) error {
	region, err := GetGCERegion(gce.localZone)
	if err != nil {
		return err
	}
	// TODO: This completely breaks modularity in the cloudprovider but the methods
	// shared with the TCPLoadBalancer take v1.ServicePorts.
	svcPorts := []v1.ServicePort{}
	// TODO: Currently the only consumer of this method is the GCE L7
	// loadbalancer controller, which never needs a protocol other than TCP.
	// We should pipe through a mapping of port:protocol and default to TCP
	// if UDP ports are required. This means the method signature will change,
	// forcing downstream clients to refactor interfaces.
	for _, p := range ports {
		svcPorts = append(svcPorts, v1.ServicePort{Port: int32(p), Protocol: v1.ProtocolTCP})
	}
	hosts, err := gce.getInstancesByNames(hostNames)
	if err != nil {
		return err
	}
	return gce.updateFirewall(name, region, desc, sourceRanges, svcPorts, hosts)
}

// Global static IP management

// ReserveGlobalStaticIP creates a global static IP.
// Caller is allocated a random IP if they do not specify an ipAddress. If an
// ipAddress is specified, it must belong to the current project, eg: an
// ephemeral IP associated with a global forwarding rule.
func (gce *Cloud) ReserveGlobalStaticIP(name, ipAddress string) (address *compute.Address, err error) {
	op, err := gce.service.GlobalAddresses.Insert(gce.projectID, &compute.Address{Name: name, Address: ipAddress}).Do()
	if err != nil {
		return nil, err
	}
	if err := gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	// We have to get the address to know which IP was allocated for us.
	return gce.service.GlobalAddresses.Get(gce.projectID, name).Do()
}

// DeleteGlobalStaticIP deletes a global static IP by name.
func (gce *Cloud) DeleteGlobalStaticIP(name string) error {
	op, err := gce.service.GlobalAddresses.Delete(gce.projectID, name).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// GetGlobalStaticIP returns the global static IP by name.
func (gce *Cloud) GetGlobalStaticIP(name string) (address *compute.Address, err error) {
	return gce.service.GlobalAddresses.Get(gce.projectID, name).Do()
}

// UrlMap management

// GetURLMap returns the URLMap by name.
func (gce *Cloud) GetURLMap(name string) (*compute.UrlMap, error) {
	return gce.service.UrlMaps.Get(gce.projectID, name).Do()
}

// CreateURLMap creates an url map, using the given backend service as the default service.
func (gce *Cloud) CreateURLMap(backend *compute.BackendService, name string) (*compute.UrlMap, error) {
	urlMap := &compute.UrlMap{
		Name:           name,
		DefaultService: backend.SelfLink,
	}
	op, err := gce.service.UrlMaps.Insert(gce.projectID, urlMap).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.GetURLMap(name)
}

// UpdateURLMap applies the given UrlMap as an update, and returns the new UrlMap.
func (gce *Cloud) UpdateURLMap(urlMap *compute.UrlMap) (*compute.UrlMap, error) {
	op, err := gce.service.UrlMaps.Update(gce.projectID, urlMap.Name, urlMap).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.service.UrlMaps.Get(gce.projectID, urlMap.Name).Do()
}

// DeleteURLMap deletes a url map by name.
func (gce *Cloud) DeleteURLMap(name string) error {
	op, err := gce.service.UrlMaps.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListURLMaps lists all URLMaps in the project.
func (gce *Cloud) ListURLMaps() (*compute.UrlMapList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.UrlMaps.List(gce.projectID).Do()
}

// TargetHttpProxy management

// GetTargetHTTPProxy returns the UrlMap by name.
func (gce *Cloud) GetTargetHTTPProxy(name string) (*compute.TargetHttpProxy, error) {
	return gce.service.TargetHttpProxies.Get(gce.projectID, name).Do()
}

// CreateTargetHTTPProxy creates and returns a TargetHTTPProxy with the given UrlMap.
func (gce *Cloud) CreateTargetHTTPProxy(urlMap *compute.UrlMap, name string) (*compute.TargetHttpProxy, error) {
	proxy := &compute.TargetHttpProxy{
		Name:   name,
		UrlMap: urlMap.SelfLink,
	}
	op, err := gce.service.TargetHttpProxies.Insert(gce.projectID, proxy).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.GetTargetHTTPProxy(name)
}

// SetURLMapForTargetHTTPProxy sets the given URLMap for the given TargetHTTPProxy.
func (gce *Cloud) SetURLMapForTargetHTTPProxy(proxy *compute.TargetHttpProxy, urlMap *compute.UrlMap) error {
	op, err := gce.service.TargetHttpProxies.SetUrlMap(gce.projectID, proxy.Name, &compute.UrlMapReference{UrlMap: urlMap.SelfLink}).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// DeleteTargetHTTPProxy deletes the TargetHTTPProxy by name.
func (gce *Cloud) DeleteTargetHTTPProxy(name string) error {
	op, err := gce.service.TargetHttpProxies.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListTargetHTTPProxies lists all TargetHTTPProxies in the project.
func (gce *Cloud) ListTargetHTTPProxies() (*compute.TargetHttpProxyList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.TargetHttpProxies.List(gce.projectID).Do()
}

// TargetHttpsProxy management

// GetTargetHTTPSProxy returns the URLMap by name.
func (gce *Cloud) GetTargetHTTPSProxy(name string) (*compute.TargetHttpsProxy, error) {
	return gce.service.TargetHttpsProxies.Get(gce.projectID, name).Do()
}

// CreateTargetHTTPSProxy creates and returns a TargetHTTPSProxy with the given URLMap and SslCertificate.
func (gce *Cloud) CreateTargetHTTPSProxy(urlMap *compute.UrlMap, sslCert *compute.SslCertificate, name string) (*compute.TargetHttpsProxy, error) {
	proxy := &compute.TargetHttpsProxy{
		Name:            name,
		UrlMap:          urlMap.SelfLink,
		SslCertificates: []string{sslCert.SelfLink},
	}
	op, err := gce.service.TargetHttpsProxies.Insert(gce.projectID, proxy).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.GetTargetHTTPSProxy(name)
}

// SetURLMapForTargetHTTPSProxy sets the given URLMap for the given TargetHTTPSProxy.
func (gce *Cloud) SetURLMapForTargetHTTPSProxy(proxy *compute.TargetHttpsProxy, urlMap *compute.UrlMap) error {
	op, err := gce.service.TargetHttpsProxies.SetUrlMap(gce.projectID, proxy.Name, &compute.UrlMapReference{UrlMap: urlMap.SelfLink}).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// SetSslCertificateForTargetHTTPSProxy sets the given SslCertificate for the given TargetHTTPSProxy.
func (gce *Cloud) SetSslCertificateForTargetHTTPSProxy(proxy *compute.TargetHttpsProxy, sslCert *compute.SslCertificate) error {
	op, err := gce.service.TargetHttpsProxies.SetSslCertificates(gce.projectID, proxy.Name, &compute.TargetHttpsProxiesSetSslCertificatesRequest{SslCertificates: []string{sslCert.SelfLink}}).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// DeleteTargetHTTPSProxy deletes the TargetHTTPSProxy by name.
func (gce *Cloud) DeleteTargetHTTPSProxy(name string) error {
	op, err := gce.service.TargetHttpsProxies.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListTargetHTTPSProxies lists all TargetHTTPSProxies in the project.
func (gce *Cloud) ListTargetHTTPSProxies() (*compute.TargetHttpsProxyList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.TargetHttpsProxies.List(gce.projectID).Do()
}

// SSL Certificate management

// GetSslCertificate returns the SslCertificate by name.
func (gce *Cloud) GetSslCertificate(name string) (*compute.SslCertificate, error) {
	return gce.service.SslCertificates.Get(gce.projectID, name).Do()
}

// CreateSslCertificate creates and returns a SslCertificate.
func (gce *Cloud) CreateSslCertificate(sslCerts *compute.SslCertificate) (*compute.SslCertificate, error) {
	op, err := gce.service.SslCertificates.Insert(gce.projectID, sslCerts).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.GetSslCertificate(sslCerts.Name)
}

// DeleteSslCertificate deletes the SslCertificate by name.
func (gce *Cloud) DeleteSslCertificate(name string) error {
	op, err := gce.service.SslCertificates.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListSslCertificates lists all SslCertificates in the project.
func (gce *Cloud) ListSslCertificates() (*compute.SslCertificateList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.SslCertificates.List(gce.projectID).Do()
}

// GlobalForwardingRule management

// CreateGlobalForwardingRule creates and returns a GlobalForwardingRule that points to the given TargetHttp(s)Proxy.
// targetProxyLink is the SelfLink of a TargetHttp(s)Proxy.
func (gce *Cloud) CreateGlobalForwardingRule(targetProxyLink, ip, name, portRange string) (*compute.ForwardingRule, error) {
	rule := &compute.ForwardingRule{
		Name:       name,
		IPAddress:  ip,
		Target:     targetProxyLink,
		PortRange:  portRange,
		IPProtocol: "TCP",
	}
	op, err := gce.service.GlobalForwardingRules.Insert(gce.projectID, rule).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForGlobalOp(op); err != nil {
		return nil, err
	}
	return gce.GetGlobalForwardingRule(name)
}

// SetProxyForGlobalForwardingRule links the given TargetHttp(s)Proxy with the given GlobalForwardingRule.
// targetProxyLink is the SelfLink of a TargetHttp(s)Proxy.
func (gce *Cloud) SetProxyForGlobalForwardingRule(fw *compute.ForwardingRule, targetProxyLink string) error {
	op, err := gce.service.GlobalForwardingRules.SetTarget(gce.projectID, fw.Name, &compute.TargetReference{Target: targetProxyLink}).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// DeleteGlobalForwardingRule deletes the GlobalForwardingRule by name.
func (gce *Cloud) DeleteGlobalForwardingRule(name string) error {
	op, err := gce.service.GlobalForwardingRules.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// GetGlobalForwardingRule returns the GlobalForwardingRule by name.
func (gce *Cloud) GetGlobalForwardingRule(name string) (*compute.ForwardingRule, error) {
	return gce.service.GlobalForwardingRules.Get(gce.projectID, name).Do()
}

// ListGlobalForwardingRules lists all GlobalForwardingRules in the project.
func (gce *Cloud) ListGlobalForwardingRules() (*compute.ForwardingRuleList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.GlobalForwardingRules.List(gce.projectID).Do()
}

// BackendService Management

// GetBackendService retrieves a backend by name.
func (gce *Cloud) GetBackendService(name string) (*compute.BackendService, error) {
	return gce.service.BackendServices.Get(gce.projectID, name).Do()
}

// UpdateBackendService applies the given BackendService as an update to an existing service.
func (gce *Cloud) UpdateBackendService(bg *compute.BackendService) error {
	op, err := gce.service.BackendServices.Update(gce.projectID, bg.Name, bg).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// DeleteBackendService deletes the given BackendService by name.
func (gce *Cloud) DeleteBackendService(name string) error {
	op, err := gce.service.BackendServices.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// CreateBackendService creates the given BackendService.
func (gce *Cloud) CreateBackendService(bg *compute.BackendService) error {
	op, err := gce.service.BackendServices.Insert(gce.projectID, bg).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListBackendServices lists all backend services in the project.
func (gce *Cloud) ListBackendServices() (*compute.BackendServiceList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.BackendServices.List(gce.projectID).Do()
}

// GetHealth returns the health of the BackendService identified by the given
// name, in the given instanceGroup. The instanceGroupLink is the fully
// qualified self link of an instance group.
func (gce *Cloud) GetHealth(name string, instanceGroupLink string) (*compute.BackendServiceGroupHealth, error) {
	groupRef := &compute.ResourceGroupReference{Group: instanceGroupLink}
	return gce.service.BackendServices.GetHealth(gce.projectID, name, groupRef).Do()
}

// Health Checks

// GetHTTPHealthCheck returns the given HTTPHealthCheck by name.
func (gce *Cloud) GetHTTPHealthCheck(name string) (*compute.HttpHealthCheck, error) {
	return gce.service.HttpHealthChecks.Get(gce.projectID, name).Do()
}

// UpdateHTTPHealthCheck applies the given HTTPHealthCheck as an update.
func (gce *Cloud) UpdateHTTPHealthCheck(hc *compute.HttpHealthCheck) error {
	op, err := gce.service.HttpHealthChecks.Update(gce.projectID, hc.Name, hc).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// DeleteHTTPHealthCheck deletes the given HTTPHealthCheck by name.
func (gce *Cloud) DeleteHTTPHealthCheck(name string) error {
	op, err := gce.service.HttpHealthChecks.Delete(gce.projectID, name).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(op)
}

// CreateHTTPHealthCheck creates the given HTTPHealthCheck.
func (gce *Cloud) CreateHTTPHealthCheck(hc *compute.HttpHealthCheck) error {
	op, err := gce.service.HttpHealthChecks.Insert(gce.projectID, hc).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(op)
}

// ListHTTPHealthChecks lists all HTTPHealthChecks in the project.
func (gce *Cloud) ListHTTPHealthChecks() (*compute.HttpHealthCheckList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.HttpHealthChecks.List(gce.projectID).Do()
}

// InstanceGroup Management

// CreateInstanceGroup creates an instance group with the given instances. It is the callers responsibility to add named ports.
func (gce *Cloud) CreateInstanceGroup(name string, zone string) (*compute.InstanceGroup, error) {
	op, err := gce.service.InstanceGroups.Insert(
		gce.projectID, zone, &compute.InstanceGroup{Name: name}).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForZoneOp(op, zone); err != nil {
		return nil, err
	}
	return gce.GetInstanceGroup(name, zone)
}

// DeleteInstanceGroup deletes an instance group.
func (gce *Cloud) DeleteInstanceGroup(name string, zone string) error {
	op, err := gce.service.InstanceGroups.Delete(
		gce.projectID, zone, name).Do()
	if err != nil {
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// ListInstanceGroups lists all InstanceGroups in the project and zone.
func (gce *Cloud) ListInstanceGroups(zone string) (*compute.InstanceGroupList, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.InstanceGroups.List(gce.projectID, zone).Do()
}

// ListInstancesInInstanceGroup lists all the instances in a given instance group and state.
func (gce *Cloud) ListInstancesInInstanceGroup(name string, zone string, state string) (*compute.InstanceGroupsListInstances, error) {
	// TODO: use PageToken to list all not just the first 500
	return gce.service.InstanceGroups.ListInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsListInstancesRequest{InstanceState: state}).Do()
}

// AddInstancesToInstanceGroup adds the given instances to the given instance group.
func (gce *Cloud) AddInstancesToInstanceGroup(name string, zone string, instanceNames []string) error {
	if len(instanceNames) == 0 {
		return nil
	}
	// Adding the same instance twice will result in a 4xx error
	instances := []*compute.InstanceReference{}
	for _, ins := range instanceNames {
		instances = append(instances, &compute.InstanceReference{Instance: makeHostURL(gce.projectID, zone, ins)})
	}
	op, err := gce.service.InstanceGroups.AddInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsAddInstancesRequest{
			Instances: instances,
		}).Do()

	if err != nil {
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// RemoveInstancesFromInstanceGroup removes the given instances from the instance group.
func (gce *Cloud) RemoveInstancesFromInstanceGroup(name string, zone string, instanceNames []string) error {
	if len(instanceNames) == 0 {
		return nil
	}
	instances := []*compute.InstanceReference{}
	for _, ins := range instanceNames {
		instanceLink := makeHostURL(gce.projectID, zone, ins)
		instances = append(instances, &compute.InstanceReference{Instance: instanceLink})
	}
	op, err := gce.service.InstanceGroups.RemoveInstances(
		gce.projectID, zone, name,
		&compute.InstanceGroupsRemoveInstancesRequest{
			Instances: instances,
		}).Do()

	if err != nil {
		if isHTTPErrorCode(err, http.StatusNotFound) {
			return nil
		}
		return err
	}
	return gce.waitForZoneOp(op, zone)
}

// AddPortToInstanceGroup adds a port to the given instance group.
func (gce *Cloud) AddPortToInstanceGroup(ig *compute.InstanceGroup, port int64) (*compute.NamedPort, error) {
	for _, np := range ig.NamedPorts {
		if np.Port == port {
			glog.V(3).Infof("Instance group %v already has named port %+v", ig.Name, np)
			return np, nil
		}
	}
	glog.Infof("Adding port %v to instance group %v with %d ports", port, ig.Name, len(ig.NamedPorts))
	namedPort := compute.NamedPort{Name: fmt.Sprintf("port%v", port), Port: port}
	ig.NamedPorts = append(ig.NamedPorts, &namedPort)

	// setNamedPorts is a zonal endpoint, meaning we invoke it by re-creating a URL like:
	// {project}/zones/{zone}/instanceGroups/{instanceGroup}/setNamedPorts, so the "zone"
	// parameter given to SetNamedPorts must not be the entire zone URL.
	zoneURLParts := strings.Split(ig.Zone, "/")
	zone := zoneURLParts[len(zoneURLParts)-1]

	op, err := gce.service.InstanceGroups.SetNamedPorts(
		gce.projectID, zone, ig.Name,
		&compute.InstanceGroupsSetNamedPortsRequest{
			NamedPorts: ig.NamedPorts}).Do()
	if err != nil {
		return nil, err
	}
	if err = gce.waitForZoneOp(op, zone); err != nil {
		return nil, err
	}
	return &namedPort, nil
}

// GetInstanceGroup returns an instance group by name.
func (gce *Cloud) GetInstanceGroup(name string, zone string) (*compute.InstanceGroup, error) {
	return gce.service.InstanceGroups.Get(gce.projectID, zone, name).Do()
}

// Take a GCE instance 'hostname' and break it down to something that can be fed
// to the GCE API client library.  Basically this means reducing 'kubernetes-
// node-2.c.my-proj.internal' to 'kubernetes-node-2' if necessary.
func canonicalizeInstanceName(name string) string {
	ix := strings.Index(name, ".")
	if ix != -1 {
		name = name[:ix]
	}
	return name
}

// CurrentNodeName is an implementation of Instances.CurrentNodeName
func (gce *Cloud) CurrentNodeName(hostname string) (types.NodeName, error) {
	return types.NodeName(hostname), nil
}

// AddSSHKeyToAllInstances adds SSH key to all instances
func (gce *Cloud) AddSSHKeyToAllInstances(user string, keyData []byte) error {
	return wait.Poll(2*time.Second, 30*time.Second, func() (bool, error) {
		project, err := gce.service.Projects.Get(gce.projectID).Do()
		if err != nil {
			glog.Errorf("Could not get project: %v", err)
			return false, nil
		}
		keyString := fmt.Sprintf("%s:%s %s@%s", user, strings.TrimSpace(string(keyData)), user, user)
		found := false
		for _, item := range project.CommonInstanceMetadata.Items {
			if item.Key == "sshKeys" {
				if strings.Contains(*item.Value, keyString) {
					// We've already added the key
					glog.Info("SSHKey already in project metadata")
					return true, nil
				}
				value := *item.Value + "\n" + keyString
				item.Value = &value
				found = true
				break
			}
		}
		if !found {
			// This is super unlikely, so log.
			glog.Infof("Failed to find sshKeys metadata, creating a new item")
			project.CommonInstanceMetadata.Items = append(project.CommonInstanceMetadata.Items,
				&compute.MetadataItems{
					Key:   "sshKeys",
					Value: &keyString,
				})
		}
		op, err := gce.service.Projects.SetCommonInstanceMetadata(gce.projectID, project.CommonInstanceMetadata).Do()
		if err != nil {
			glog.Errorf("Could not Set Metadata: %v", err)
			return false, nil
		}
		if err := gce.waitForGlobalOp(op); err != nil {
			glog.Errorf("Could not Set Metadata: %v", err)
			return false, nil
		}
		glog.Infof("Successfully added sshKey to project metadata")
		return true, nil
	})
}

// NodeAddresses is an implementation of Instances.NodeAddresses.
func (gce *Cloud) NodeAddresses(_ types.NodeName) ([]v1.NodeAddress, error) {
	internalIP, err := metadata.Get("instance/network-interfaces/0/ip")
	if err != nil {
		return nil, fmt.Errorf("couldn't get internal IP: %v", err)
	}
	externalIP, err := metadata.Get("instance/network-interfaces/0/access-configs/0/external-ip")
	if err != nil {
		return nil, fmt.Errorf("couldn't get external IP: %v", err)
	}
	return []v1.NodeAddress{
		{Type: v1.NodeInternalIP, Address: internalIP},
		{Type: v1.NodeExternalIP, Address: externalIP},
	}, nil
}

// isCurrentInstance uses metadata server to check if specified instanceID matches current machine's instanceID
func (gce *Cloud) isCurrentInstance(instanceID string) bool {
	currentInstanceID, err := getInstanceIDViaMetadata()
	if err != nil {
		// Log and swallow error
		glog.Errorf("Failed to fetch instanceID via Metadata: %v", err)
		return false
	}

	return currentInstanceID == canonicalizeInstanceName(instanceID)
}

// mapNodeNameToInstanceName maps a k8s NodeName to a GCE Instance Name
// This is a simple string cast.
func mapNodeNameToInstanceName(nodeName types.NodeName) string {
	return string(nodeName)
}

// ExternalID returns the cloud provider ID of the node with the specified NodeName (deprecated).
func (gce *Cloud) ExternalID(nodeName types.NodeName) (string, error) {
	instanceName := mapNodeNameToInstanceName(nodeName)
	if gce.useMetadataServer {
		// Use metadata, if possible, to fetch ID. See issue #12000
		if gce.isCurrentInstance(instanceName) {
			externalInstanceID, err := getCurrentExternalIDViaMetadata()
			if err == nil {
				return externalInstanceID, nil
			}
		}
	}

	// Fallback to GCE API call if metadata server fails to retrieve ID
	inst, err := gce.getInstanceByName(instanceName)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(inst.ID, 10), nil
}

// InstanceID returns the cloud provider ID of the node with the specified NodeName.
func (gce *Cloud) InstanceID(nodeName types.NodeName) (string, error) {
	instanceName := mapNodeNameToInstanceName(nodeName)
	if gce.useMetadataServer {
		// Use metadata, if possible, to fetch ID. See issue #12000
		if gce.isCurrentInstance(instanceName) {
			projectID, zone, err := getProjectAndZone()
			if err == nil {
				return projectID + "/" + zone + "/" + canonicalizeInstanceName(instanceName), nil
			}
		}
	}
	instance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		return "", err
	}
	return gce.projectID + "/" + instance.Zone + "/" + instance.Name, nil
}

// InstanceType returns the type of the specified node with the specified NodeName.
func (gce *Cloud) InstanceType(nodeName types.NodeName) (string, error) {
	instanceName := mapNodeNameToInstanceName(nodeName)
	if gce.useMetadataServer {
		// Use metadata, if possible, to fetch ID. See issue #12000
		if gce.isCurrentInstance(instanceName) {
			mType, err := getCurrentMachineTypeViaMetadata()
			if err == nil {
				return mType, nil
			}
		}
	}
	instance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		return "", err
	}
	return instance.Type, nil
}

// GetAllZones returns all the zones in which nodes are running
func (gce *Cloud) GetAllZones() (sets.String, error) {
	// Fast-path for non-multizone
	if len(gce.managedZones) == 1 {
		return sets.NewString(gce.managedZones...), nil
	}

	// TODO: Caching, but this is currently only called when we are creating a volume,
	// which is a relatively infrequent operation, and this is only # zones API calls
	zones := sets.NewString()

	// TODO: Parallelize, although O(zones) so not too bad (N <= 3 typically)
	for _, zone := range gce.managedZones {
		// We only retrieve one page in each zone - we only care about existence
		listCall := gce.service.Instances.List(gce.projectID, zone)

		// No filter: We assume that a zone is either used or unused
		// We could only consider running nodes (like we do in List above),
		// but probably if instances are starting we still want to consider them.
		// I think we should wait until we have a reason to make the
		// call one way or the other; we generally can't guarantee correct
		// volume spreading if the set of zones is changing
		// (and volume spreading is currently only a heuristic).
		// Long term we want to replace GetAllZones (which primarily supports volume
		// spreading) with a scheduler policy that is able to see the global state of
		// volumes and the health of zones.

		// Just a minimal set of fields - we only care about existence
		listCall = listCall.Fields("items(name)")

		res, err := listCall.Do()
		if err != nil {
			return nil, err
		}
		if len(res.Items) != 0 {
			zones.Insert(zone)
		}
	}

	return zones, nil
}

func truncateClusterName(clusterName string) string {
	if len(clusterName) > 26 {
		return clusterName[:26]
	}
	return clusterName
}

// ListRoutes lists routes
func (gce *Cloud) ListRoutes(clusterName string) ([]*cloudprovider.Route, error) {
	var routes []*cloudprovider.Route
	pageToken := ""
	page := 0
	for ; page == 0 || (pageToken != "" && page < maxPages); page++ {
		listCall := gce.service.Routes.List(gce.projectID)

		prefix := truncateClusterName(clusterName)
		listCall = listCall.Filter("name eq " + prefix + "-.*")
		if pageToken != "" {
			listCall = listCall.PageToken(pageToken)
		}
		res, err := listCall.Do()
		if err != nil {
			glog.Errorf("Error getting routes from GCE: %v", err)
			return nil, err
		}
		pageToken = res.NextPageToken
		for _, r := range res.Items {
			if r.Network != gce.networkURL {
				continue
			}
			// Not managed if route description != "k8s-node-route"
			if r.Description != k8sNodeRouteTag {
				continue
			}
			// Not managed if route name doesn't start with <clusterName>
			if !strings.HasPrefix(r.Name, prefix) {
				continue
			}

			target := path.Base(r.NextHopInstance)
			// TODO: Should we lastComponent(target) this?
			targetNodeName := types.NodeName(target) // NodeName == Instance Name on GCE
			routes = append(routes, &cloudprovider.Route{Name: r.Name, TargetNode: targetNodeName, DestinationCIDR: r.DestRange})
		}
	}
	if page >= maxPages {
		glog.Errorf("ListRoutes exceeded maxPages=%d for Routes.List; truncating.", maxPages)
	}
	return routes, nil
}

func gceNetworkURL(project, network string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/networks/%s", project, network)
}

// CreateRoute creates a route
func (gce *Cloud) CreateRoute(clusterName string, nameHint string, route *cloudprovider.Route) error {
	routeName := truncateClusterName(clusterName) + "-" + nameHint

	instanceName := mapNodeNameToInstanceName(route.TargetNode)
	targetInstance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		return err
	}
	insertOp, err := gce.service.Routes.Insert(gce.projectID, &compute.Route{
		Name:            routeName,
		DestRange:       route.DestinationCIDR,
		NextHopInstance: fmt.Sprintf("zones/%s/instances/%s", targetInstance.Zone, targetInstance.Name),
		Network:         gce.networkURL,
		Priority:        1000,
		Description:     k8sNodeRouteTag,
	}).Do()
	if err != nil {
		if isHTTPErrorCode(err, http.StatusConflict) {
			glog.Infof("Route %v already exists.", routeName)
			return nil
		}
		return err
	}
	return gce.waitForGlobalOp(insertOp)
}

// DeleteRoute deletes a route
func (gce *Cloud) DeleteRoute(clusterName string, route *cloudprovider.Route) error {
	deleteOp, err := gce.service.Routes.Delete(gce.projectID, route.Name).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(deleteOp)
}

// GetZone gets a zone
func (gce *Cloud) GetZone() (cloudprovider.Zone, error) {
	return cloudprovider.Zone{
		FailureDomain: gce.localZone,
		Region:        gce.region,
	}, nil
}

// encodeDiskTags encodes requested volume tags into JSON string, as GCE does
// not support tags on GCE PDs and we use Description field as fallback.
func (gce *Cloud) encodeDiskTags(tags map[string]string) (string, error) {
	if len(tags) == 0 {
		// No tags -> empty JSON
		return "", nil
	}

	enc, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}

// CreateDisk creates a new Persistent Disk, with the specified name & size, in
// the specified zone. It stores specified tags encoded in JSON in Description
// field.
func (gce *Cloud) CreateDisk(name string, diskType string, zone string, sizeGb int64, tags map[string]string) error {
	// Do not allow creation of PDs in zones that are not managed. Such PDs
	// then cannot be deleted by DeleteDisk.
	isManaged := false
	for _, managedZone := range gce.managedZones {
		if zone == managedZone {
			isManaged = true
			break
		}
	}
	if !isManaged {
		return fmt.Errorf("kubernetes does not manage zone %q", zone)
	}

	tagsStr, err := gce.encodeDiskTags(tags)
	if err != nil {
		return err
	}

	switch diskType {
	case DiskTypeSSD, DiskTypeStandard:
		// noop
	case "":
		diskType = diskTypeDefault
	default:
		return fmt.Errorf("invalid GCE disk type %q", diskType)
	}
	diskTypeURI := fmt.Sprintf(diskTypeURITemplate, gce.projectID, zone, diskType)

	diskToCreate := &compute.Disk{
		Name:        name,
		SizeGb:      sizeGb,
		Description: tagsStr,
		Type:        diskTypeURI,
	}

	createOp, err := gce.service.Disks.Insert(gce.projectID, zone, diskToCreate).Do()
	if err != nil {
		return err
	}

	err = gce.waitForZoneOp(createOp, zone)
	if isGCEError(err, "alreadyExists") {
		glog.Warningf("GCE PD %q already exists, reusing", name)
		return nil
	}
	return err
}

func (gce *Cloud) doDeleteDisk(diskToDelete string) error {
	disk, err := gce.getDiskByNameUnknownZone(diskToDelete)
	if err != nil {
		return err
	}

	deleteOp, err := gce.service.Disks.Delete(gce.projectID, disk.Zone, disk.Name).Do()
	if err != nil {
		return err
	}

	return gce.waitForZoneOp(deleteOp, disk.Zone)
}

// DeleteDisk deletes a disk
func (gce *Cloud) DeleteDisk(diskToDelete string) error {
	err := gce.doDeleteDisk(diskToDelete)
	if isGCEError(err, "resourceInUseByAnotherResource") {
		return volume.NewDeletedVolumeInUseError(err.Error())
	}

	if err == cloudprovider.ErrDiskNotFound {
		return nil
	}
	return err
}

// isGCEError returns true if given error is a googleapi.Error with given
// reason (e.g. "resourceInUseByAnotherResource")
func isGCEError(err error, reason string) bool {
	apiErr, ok := err.(*googleapi.Error)
	if !ok {
		return false
	}

	for _, e := range apiErr.Errors {
		if e.Reason == reason {
			return true
		}
	}
	return false
}

// GetAutoLabelsForPD builds the labels that should be automatically added to a PersistentVolume backed by a GCE PD
// Specifically, this builds FailureDomain (zone) and Region labels.
// The PersistentVolumeLabel admission controller calls this and adds the labels when a PV is created.
// If zone is specified, the volume will only be found in the specified zone,
// otherwise all managed zones will be searched.
func (gce *Cloud) GetAutoLabelsForPD(name string, zone string) (map[string]string, error) {
	var disk *gceDisk
	var err error
	if zone == "" {
		// We would like as far as possible to avoid this case,
		// because GCE doesn't guarantee that volumes are uniquely named per region,
		// just per zone.  However, creation of GCE PDs was originally done only
		// by name, so we have to continue to support that.
		// However, wherever possible the zone should be passed (and it is passed
		// for most cases that we can control, e.g. dynamic volume provisioning)
		disk, err = gce.getDiskByNameUnknownZone(name)
		if err != nil {
			return nil, err
		}
		zone = disk.Zone
	} else {
		// We could assume the disks exists; we have all the information we need
		// However it is more consistent to ensure the disk exists,
		// and in future we may gather addition information (e.g. disk type, IOPS etc)
		disk, err = gce.getDiskByName(name, zone)
		if err != nil {
			return nil, err
		}
	}

	region, err := GetGCERegion(zone)
	if err != nil {
		return nil, err
	}

	if zone == "" || region == "" {
		// Unexpected, but sanity-check
		return nil, fmt.Errorf("PD did not have zone/region information: %q", disk.Name)
	}

	labels := make(map[string]string)
	labels[apis.LabelZoneFailureDomain] = zone
	labels[apis.LabelZoneRegion] = region

	return labels, nil
}

// AttachDisk attaches a disk
func (gce *Cloud) AttachDisk(diskName string, nodeName types.NodeName, readOnly bool) error {
	instanceName := mapNodeNameToInstanceName(nodeName)
	instance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		return fmt.Errorf("error getting instance %q", instanceName)
	}
	disk, err := gce.getDiskByName(diskName, instance.Zone)
	if err != nil {
		return err
	}
	readWrite := "READ_WRITE"
	if readOnly {
		readWrite = "READ_ONLY"
	}
	attachedDisk := gce.convertDiskToAttachedDisk(disk, readWrite)

	attachOp, err := gce.service.Instances.AttachDisk(gce.projectID, disk.Zone, instance.Name, attachedDisk).Do()
	if err != nil {
		return err
	}

	return gce.waitForZoneOp(attachOp, disk.Zone)
}

// DetachDisk detaches a disk
func (gce *Cloud) DetachDisk(devicePath string, nodeName types.NodeName) error {
	instanceName := mapNodeNameToInstanceName(nodeName)
	inst, err := gce.getInstanceByName(instanceName)
	if err != nil {
		if err == cloudprovider.ErrInstanceNotFound {
			// If instance no longer exists, safe to assume volume is not attached.
			glog.Warningf(
				"Instance %q does not exist. DetachDisk will assume PD %q is not attached to it.",
				instanceName,
				devicePath)
			return nil
		}

		return fmt.Errorf("error getting instance %q", instanceName)
	}

	detachOp, err := gce.service.Instances.DetachDisk(gce.projectID, inst.Zone, inst.Name, devicePath).Do()
	if err != nil {
		return err
	}

	return gce.waitForZoneOp(detachOp, inst.Zone)
}

// DiskIsAttached checks if disk is attached
func (gce *Cloud) DiskIsAttached(diskName string, nodeName types.NodeName) (bool, error) {
	instanceName := mapNodeNameToInstanceName(nodeName)
	instance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		if err == cloudprovider.ErrInstanceNotFound {
			// If instance no longer exists, safe to assume volume is not attached.
			glog.Warningf(
				"Instance %q does not exist. DiskIsAttached will assume PD %q is not attached to it.",
				instanceName,
				diskName)
			return false, nil
		}

		return false, err
	}

	for _, disk := range instance.Disks {
		if disk.DeviceName == diskName {
			// Disk is still attached to node
			return true, nil
		}
	}

	return false, nil
}

// DisksAreAttached checks if disks are attached
func (gce *Cloud) DisksAreAttached(diskNames []string, nodeName types.NodeName) (map[string]bool, error) {
	attached := make(map[string]bool)
	for _, diskName := range diskNames {
		attached[diskName] = false
	}
	instanceName := mapNodeNameToInstanceName(nodeName)
	instance, err := gce.getInstanceByName(instanceName)
	if err != nil {
		if err == cloudprovider.ErrInstanceNotFound {
			// If instance no longer exists, safe to assume volume is not attached.
			glog.Warningf(
				"Instance %q does not exist. DisksAreAttached will assume PD %v are not attached to it.",
				instanceName,
				diskNames)
			return attached, nil
		}

		return attached, err
	}

	for _, instanceDisk := range instance.Disks {
		for _, diskName := range diskNames {
			if instanceDisk.DeviceName == diskName {
				// Disk is still attached to node
				attached[diskName] = true
			}
		}
	}

	return attached, nil
}

// Returns a gceDisk for the disk, if it is found in the specified zone.
// If not found, returns (nil, nil)
func (gce *Cloud) findDiskByName(diskName string, zone string) (*gceDisk, error) {
	disk, err := gce.service.Disks.Get(gce.projectID, zone, diskName).Do()
	if err == nil {
		d := &gceDisk{
			Zone: lastComponent(disk.Zone),
			Name: disk.Name,
			Kind: disk.Kind,
		}
		return d, nil
	}
	if !isHTTPErrorCode(err, http.StatusNotFound) {
		return nil, err
	}
	return nil, nil
}

// Like findDiskByName, but returns an error if the disk is not found
func (gce *Cloud) getDiskByName(diskName string, zone string) (*gceDisk, error) {
	disk, err := gce.findDiskByName(diskName, zone)
	if disk == nil && err == nil {
		return nil, fmt.Errorf("GCE persistent disk not found: diskName=%q zone=%q", diskName, zone)
	}
	return disk, err
}

// Scans all managed zones to return the GCE PD
// Prefer getDiskByName, if the zone can be established
// Return cloudprovider.ErrDiskNotFound if the given disk cannot be found in any zone
func (gce *Cloud) getDiskByNameUnknownZone(diskName string) (*gceDisk, error) {
	// Note: this is the gotcha right now with GCE PD support:
	// disk names are not unique per-region.
	// (I can create two volumes with name "myvol" in e.g. us-central1-b & us-central1-f)
	// For now, this is simply undefined behvaiour.
	//
	// In future, we will have to require users to qualify their disk
	// "us-central1-a/mydisk".  We could do this for them as part of
	// admission control, but that might be a little weird (values changing
	// on create)

	var found *gceDisk
	for _, zone := range gce.managedZones {
		disk, err := gce.findDiskByName(diskName, zone)
		if err != nil {
			return nil, err
		}
		// findDiskByName returns (nil,nil) if the disk doesn't exist, so we can't
		// assume that a disk was found unless disk is non-nil.
		if disk == nil {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("GCE persistent disk name was found in multiple zones: %q", diskName)
		}
		found = disk
	}
	if found != nil {
		return found, nil
	}
	glog.Warningf("GCE persistent disk %q not found in managed zones (%s)", diskName, strings.Join(gce.managedZones, ","))
	return nil, cloudprovider.ErrDiskNotFound
}

// GetGCERegion returns region of the gce zone. Zone names
// are of the form: ${region-name}-${ix}.
// For example, "us-central1-b" has a region of "us-central1".
// So we look for the last '-' and trim to just before that.
func GetGCERegion(zone string) (string, error) {
	ix := strings.LastIndex(zone, "-")
	if ix == -1 {
		return "", fmt.Errorf("unexpected zone: %s", zone)
	}
	return zone[:ix], nil
}

// Converts a Disk resource to an AttachedDisk resource.
func (gce *Cloud) convertDiskToAttachedDisk(disk *gceDisk, readWrite string) *compute.AttachedDisk {
	return &compute.AttachedDisk{
		DeviceName: disk.Name,
		Kind:       disk.Kind,
		Mode:       readWrite,
		Source:     "https://" + path.Join("www.googleapis.com/compute/v1/projects/", gce.projectID, "zones", disk.Zone, "disks", disk.Name),
		Type:       "PERSISTENT",
	}
}

type gceSnapshot struct {
	Name   string
	Kind   string
	Status string
}

// CreateDiskFromSnapshot create a disk from snapshot
func (gce *Cloud) CreateDiskFromSnapshot(snapshot string,
	name string, diskType string, zone string, sizeGb int64, tags map[string]string) error {

	// Do not allow creation of PDs in zones that are not managed. Such PDs
	// then cannot be deleted by DeleteDisk.
	isManaged := false
	for _, managedZone := range gce.managedZones {
		if zone == managedZone {
			isManaged = true
			break
		}
	}
	if !isManaged {
		return fmt.Errorf("kubernetes does not manage zone %q", zone)
	}

	tagsStr, err := gce.encodeDiskTags(tags)
	if err != nil {
		return fmt.Errorf("encode disk tag error %v", err)
	}

	switch diskType {
	case DiskTypeSSD, DiskTypeStandard:
		// noop
	case "":
		diskType = diskTypeDefault
	default:
		return fmt.Errorf("invalid GCE disk type %q", diskType)
	}
	diskTypeURI := fmt.Sprintf(diskTypeURITemplate, gce.projectID, zone, diskType)

	snapshotName := "global/snapshots/" + snapshot
	diskToCreate := &compute.Disk{
		Name:           name,
		SizeGb:         sizeGb,
		Description:    tagsStr,
		Type:           diskTypeURI,
		SourceSnapshot: snapshotName,
	}
	glog.Infof("Create disk from snapshot diskToCreate %+v", diskToCreate)
	createOp, err := gce.service.Disks.Insert(gce.projectID, zone, diskToCreate).Do()
	glog.Infof("Create disk from snapshot operation %v, err %v", createOp, err)
	if err != nil {
		if isGCEError(err, "alreadyExists") {
			glog.Warningf("GCE PD %q already exists, reusing", name)
			return nil
		}
		return err
	}

	err = gce.waitForZoneOp(createOp, zone)
	if isGCEError(err, "alreadyExists") {
		glog.Warningf("GCE PD %q already exists, reusing", name)
		return nil
	}
	return err
}

// DescribeSnapshot checks the status of a snapshot
func (gce *Cloud) DescribeSnapshot(snapshotToGet string) (status string, isCompleted bool, err error) {
	snapshot, err := gce.getSnapshotByName(snapshotToGet)
	if err != nil {
		return "", false, err
	}
	//no snapshot is found
	if snapshot == nil {
		return "", false, fmt.Errorf("snapshot %s is not found", snapshotToGet)
	}
	if snapshot.Status == "READY" {
		return snapshot.Status, true, nil
	}
	return snapshot.Status, false, nil
}

// FindSnapshot returns the found snapshots
func (gce *Cloud) FindSnapshot(tags map[string]string) ([]string, []string, error) {
	var snapshotIDs, statuses []string
	return snapshotIDs, statuses, nil
}

// DeleteSnapshot deletes a snapshot
func (gce *Cloud) DeleteSnapshot(snapshotToDelete string) error {

	snapshot, err := gce.getSnapshotByName(snapshotToDelete)
	if err != nil {
		return err
	}
	//no snapshot is found
	if snapshot == nil {
		return nil
	}

	deleteOp, err := gce.service.Snapshots.Delete(gce.projectID, snapshotToDelete).Do()
	if err != nil {
		return err
	}
	return gce.waitForGlobalOp(deleteOp)
}

func (gce *Cloud) getSnapshotByName(snapshotName string) (*gceSnapshot, error) {
	snapshot, err := gce.service.Snapshots.Get(gce.projectID, snapshotName).Do()
	if err == nil {
		s := &gceSnapshot{
			Name:   snapshot.Name,
			Kind:   snapshot.Kind,
			Status: snapshot.Status,
		}
		return s, nil
	}
	if !isHTTPErrorCode(err, http.StatusNotFound) {
		return nil, err
	}
	return nil, nil
}

// CreateSnapshot creates a snapshot
func (gce *Cloud) CreateSnapshot(diskName string, zone string, snapshotName string, tags map[string]string) error {
	isManaged := false
	for _, managedZone := range gce.managedZones {
		if zone == managedZone {
			isManaged = true
			break
		}
	}
	if !isManaged {
		return fmt.Errorf("kubernetes does not manage zone %q", zone)
	}
	tagsStr, err := gce.encodeDiskTags(tags)
	if err != nil {
		glog.Infof("CreateSnapshot err %v", err)
		return err
	}

	snapshotToCreate := &compute.Snapshot{
		Name:        snapshotName,
		Description: tagsStr,
	}
	glog.V(4).Infof("Create snapshot project %s, zone %s, diskName %s, snapshotToCreate %+v", gce.projectID, zone, diskName, snapshotToCreate)
	createOp, err := gce.service.Disks.CreateSnapshot(gce.projectID, zone, diskName, snapshotToCreate).Do()
	glog.V(4).Infof("Create snapshot operation %v", createOp)
	return err
}

func (gce *Cloud) listClustersInZone(zone string) ([]string, error) {
	// TODO: use PageToken to list all not just the first 500
	list, err := gce.containerService.Projects.Zones.Clusters.List(gce.projectID, zone).Do()
	if err != nil {
		return nil, err
	}
	result := []string{}
	for _, cluster := range list.Clusters {
		result = append(result, cluster.Name)
	}
	return result, nil
}

// ListClusters lists clusters
func (gce *Cloud) ListClusters() ([]string, error) {
	allClusters := []string{}

	for _, zone := range gce.managedZones {
		clusters, err := gce.listClustersInZone(zone)
		if err != nil {
			return nil, err
		}
		// TODO: Scoping?  Do we need to qualify the cluster name?
		allClusters = append(allClusters, clusters...)
	}

	return allClusters, nil
}

// Master returns k8s-<cluster name>-master.internal
func (gce *Cloud) Master(clusterName string) (string, error) {
	return "k8s-" + clusterName + "-master.internal", nil
}

type gceInstance struct {
	Zone  string
	Name  string
	ID    uint64
	Disks []*compute.AttachedDisk
	Type  string
}

type gceDisk struct {
	Zone string
	Name string
	Kind string
}

// Gets the named instances, returning cloudprovider.ErrInstanceNotFound if any instance is not found
func (gce *Cloud) getInstancesByNames(names []string) ([]*gceInstance, error) {
	instances := make(map[string]*gceInstance)
	remaining := len(names)

	nodeInstancePrefix := gce.nodeInstancePrefix
	for _, name := range names {
		name = canonicalizeInstanceName(name)
		if !strings.HasPrefix(name, gce.nodeInstancePrefix) {
			glog.Warningf("instance '%s' does not conform to prefix '%s', removing filter", name, gce.nodeInstancePrefix)
			nodeInstancePrefix = ""
		}
		instances[name] = nil
	}

	for _, zone := range gce.managedZones {
		if remaining == 0 {
			break
		}

		pageToken := ""
		page := 0
		for ; page == 0 || (pageToken != "" && page < maxPages); page++ {
			listCall := gce.service.Instances.List(gce.projectID, zone)

			if nodeInstancePrefix != "" {
				// Add the filter for hosts
				listCall = listCall.Filter("name eq " + nodeInstancePrefix + ".*")
			}

			// TODO(zmerlynn): Internal bug 29524655
			// listCall = listCall.Fields("items(name,id,disks,machineType)")
			if pageToken != "" {
				listCall.PageToken(pageToken)
			}

			res, err := listCall.Do()
			if err != nil {
				return nil, err
			}
			pageToken = res.NextPageToken
			for _, i := range res.Items {
				name := i.Name
				if _, ok := instances[name]; !ok {
					continue
				}

				instance := &gceInstance{
					Zone:  zone,
					Name:  name,
					ID:    i.Id,
					Disks: i.Disks,
					Type:  lastComponent(i.MachineType),
				}
				instances[name] = instance
				remaining--
			}
		}
		if page >= maxPages {
			glog.Errorf("getInstancesByNames exceeded maxPages=%d for Instances.List: truncating.", maxPages)
		}
	}

	instanceArray := make([]*gceInstance, len(names))
	for i, name := range names {
		name = canonicalizeInstanceName(name)
		instance := instances[name]
		if instance == nil {
			glog.Errorf("Failed to retrieve instance: %q", name)
			return nil, cloudprovider.ErrInstanceNotFound
		}
		instanceArray[i] = instances[name]
	}

	return instanceArray, nil
}

// Gets the named instance, returning cloudprovider.ErrInstanceNotFound if the instance is not found
func (gce *Cloud) getInstanceByName(name string) (*gceInstance, error) {
	// Avoid changing behaviour when not managing multiple zones
	for _, zone := range gce.managedZones {
		name = canonicalizeInstanceName(name)
		res, err := gce.service.Instances.Get(gce.projectID, zone, name).Do()
		if err != nil {
			glog.Errorf("getInstanceByName: failed to get instance %s; err: %v", name, err)
			if isHTTPErrorCode(err, http.StatusNotFound) {
				continue
			}
			return nil, err
		}
		return &gceInstance{
			Zone:  lastComponent(res.Zone),
			Name:  res.Name,
			ID:    res.Id,
			Disks: res.Disks,
			Type:  lastComponent(res.MachineType),
		}, nil
	}

	return nil, cloudprovider.ErrInstanceNotFound
}

// Returns the last component of a URL, i.e. anything after the last slash
// If there is no slash, returns the whole string
func lastComponent(s string) string {
	lastSlash := strings.LastIndex(s, "/")
	if lastSlash != -1 {
		s = s[lastSlash+1:]
	}
	return s
}
