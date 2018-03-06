/*
Copyright 2015 The Kubernetes Authors.

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

package iptables

//
// NOTE: this needs to be tested in e2e since it uses iptables for everything.
//

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/glog"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	apiservice "k8s.io/kubernetes/pkg/api/service"
	api "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/apis/core/helper"
	"k8s.io/kubernetes/pkg/proxy"
	"k8s.io/kubernetes/pkg/proxy/healthcheck"
	"k8s.io/kubernetes/pkg/proxy/metrics"
	utilproxy "k8s.io/kubernetes/pkg/proxy/util"
	"k8s.io/kubernetes/pkg/util/async"
	utiliptables "k8s.io/kubernetes/pkg/util/iptables"
	utilsysctl "k8s.io/kubernetes/pkg/util/sysctl"
	utilversion "k8s.io/kubernetes/pkg/util/version"
	utilexec "k8s.io/utils/exec"
)

const (
	// iptablesMinVersion is the minimum version of iptables for which we will use the Proxier
	// from this package instead of the userspace Proxier.  While most of the
	// features we need were available earlier, the '-C' flag was added more
	// recently.  We use that indirectly in Ensure* functions, and if we don't
	// have it, we have to be extra careful about the exact args we feed in being
	// the same as the args we read back (iptables itself normalizes some args).
	// This is the "new" Proxier, so we require "new" versions of tools.
	iptablesMinVersion = utiliptables.MinCheckVersion

	// the services chain
	kubeServicesChain utiliptables.Chain = "KUBE-SERVICES"

	// the nodeports chain
	kubeNodePortsChain utiliptables.Chain = "KUBE-NODEPORTS"

	// the kubernetes postrouting chain
	kubePostroutingChain utiliptables.Chain = "KUBE-POSTROUTING"

	// the mark-for-masquerade chain
	KubeMarkMasqChain utiliptables.Chain = "KUBE-MARK-MASQ"

	// the mark-for-drop chain
	KubeMarkDropChain utiliptables.Chain = "KUBE-MARK-DROP"

	// the kubernetes forward chain
	kubeForwardChain utiliptables.Chain = "KUBE-FORWARD"
)

// IPTablesVersioner can query the current iptables version.
type IPTablesVersioner interface {
	// returns "X.Y.Z"
	GetVersion() (string, error)
}

// KernelCompatTester tests whether the required kernel capabilities are
// present to run the iptables proxier.
type KernelCompatTester interface {
	IsCompatible() error
}

// CanUseIPTablesProxier returns true if we should use the iptables Proxier
// instead of the "classic" userspace Proxier.  This is determined by checking
// the iptables version and for the existence of kernel features. It may return
// an error if it fails to get the iptables version without error, in which
// case it will also return false.
func CanUseIPTablesProxier(iptver IPTablesVersioner, kcompat KernelCompatTester) (bool, error) {
	minVersion, err := utilversion.ParseGeneric(iptablesMinVersion)
	if err != nil {
		return false, err
	}
	versionString, err := iptver.GetVersion()
	if err != nil {
		return false, err
	}
	version, err := utilversion.ParseGeneric(versionString)
	if err != nil {
		return false, err
	}
	if version.LessThan(minVersion) {
		return false, nil
	}

	// Check that the kernel supports what we need.
	if err := kcompat.IsCompatible(); err != nil {
		return false, err
	}
	return true, nil
}

type LinuxKernelCompatTester struct{}

func (lkct LinuxKernelCompatTester) IsCompatible() error {
	// Check for the required sysctls.  We don't care about the value, just
	// that it exists.  If this Proxier is chosen, we'll initialize it as we
	// need.
	_, err := utilsysctl.New().GetSysctl(sysctlRouteLocalnet)
	return err
}

const sysctlRouteLocalnet = "net/ipv4/conf/all/route_localnet"
const sysctlBridgeCallIPTables = "net/bridge/bridge-nf-call-iptables"

// internal struct for string service information
type serviceInfo struct {
	clusterIP                net.IP
	port                     int
	protocol                 api.Protocol
	nodePort                 int
	loadBalancerStatus       api.LoadBalancerStatus
	sessionAffinityType      api.ServiceAffinity
	stickyMaxAgeSeconds      int
	externalIPs              []string
	loadBalancerSourceRanges []string
	onlyNodeLocalEndpoints   bool
	healthCheckNodePort      int
	// The following fields are computed and stored for performance reasons.
	serviceNameString        string
	servicePortChainName     utiliptables.Chain
	serviceFirewallChainName utiliptables.Chain
	serviceLBChainName       utiliptables.Chain
}

// returns a new proxy.ServicePort which abstracts a serviceInfo
func newServiceInfo(port *api.ServicePort, service *api.Service) proxy.ServicePort {
	onlyNodeLocalEndpoints := false
	if apiservice.RequestsOnlyLocalTraffic(service) {
		onlyNodeLocalEndpoints = true
	}
	var stickyMaxAgeSeconds int
	if service.Spec.SessionAffinity == api.ServiceAffinityClientIP {
		// Kube-apiserver side guarantees SessionAffinityConfig won't be nil when session affinity type is ClientIP
		stickyMaxAgeSeconds = int(*service.Spec.SessionAffinityConfig.ClientIP.TimeoutSeconds)
	}
	info := &serviceInfo{
		clusterIP: net.ParseIP(service.Spec.ClusterIP),
		port:      int(port.Port),
		protocol:  port.Protocol,
		nodePort:  int(port.NodePort),
		// Deep-copy in case the service instance changes
		loadBalancerStatus:       *helper.LoadBalancerStatusDeepCopy(&service.Status.LoadBalancer),
		sessionAffinityType:      service.Spec.SessionAffinity,
		stickyMaxAgeSeconds:      stickyMaxAgeSeconds,
		externalIPs:              make([]string, len(service.Spec.ExternalIPs)),
		loadBalancerSourceRanges: make([]string, len(service.Spec.LoadBalancerSourceRanges)),
		onlyNodeLocalEndpoints:   onlyNodeLocalEndpoints,
	}

	copy(info.loadBalancerSourceRanges, service.Spec.LoadBalancerSourceRanges)
	copy(info.externalIPs, service.Spec.ExternalIPs)

	svcName := types.NamespacedName{Namespace: service.Namespace, Name: service.Name}
	svcPortName := proxy.ServicePortName{NamespacedName: svcName, Port: port.Name}

	if apiservice.NeedsHealthCheck(service) {
		p := service.Spec.HealthCheckNodePort
		if p == 0 {
			glog.Errorf("Service %q has no healthcheck nodeport", svcName.String())
		} else {
			info.healthCheckNodePort = int(p)
		}
	}

	// Store the following for performance reasons.
	protocol := strings.ToLower(string(info.protocol))
	info.serviceNameString = svcPortName.String()
	info.servicePortChainName = servicePortChainName(info.serviceNameString, protocol)
	info.serviceFirewallChainName = serviceFirewallChainName(info.serviceNameString, protocol)
	info.serviceLBChainName = serviceLBChainName(info.serviceNameString, protocol)

	return info
}

// ClusterIP is part of proxy.ServicePort interface.
func (info *serviceInfo) ClusterIP() string {
	return info.clusterIP.String()
}

// Port is part of proxy.ServicePort interface.
func (info *serviceInfo) Port() int {
	return info.port
}

// Protocol is part of proxy.ServicePort interface.
func (info *serviceInfo) Protocol() api.Protocol {
	return info.protocol
}

// String is part of proxy.ServicePort interface.
func (info *serviceInfo) String() string {
	return fmt.Sprintf("%s:%d/%s", info.clusterIP, info.port, info.protocol)
}

// HealthCheckNodePort is part of proxy.ServicePort interface.
func (info *serviceInfo) HealthCheckNodePort() int {
	return info.healthCheckNodePort
}

var _ proxy.ServicePort = &serviceInfo{}

// internal struct for endpoints information
type endpointsInfo struct {
	endpoint string // TODO: should be an endpointString type
	isLocal  bool
	// The following fields we lazily compute and store here for performance
	// reasons. If the protocol is the same as you expect it to be, then the
	// chainName can be reused, otherwise it should be recomputed.
	protocol  string
	chainName utiliptables.Chain
}

// returns a new proxy.Endpoint which abstracts a endpointsInfo
func newEndpointsInfo(IP string, port int, isLocal bool) proxy.Endpoint {
	return &endpointsInfo{
		endpoint: net.JoinHostPort(IP, strconv.Itoa(port)),
		isLocal:  isLocal,
	}
}

// IsLocal is part of proxy.Endpoint interface.
func (e *endpointsInfo) IsLocal() bool {
	return e.isLocal
}

// IP is part of proxy.Endpoint interface.
func (e *endpointsInfo) IP() string {
	return utilproxy.IPPart(e.endpoint)
}

// Equal is part of proxy.Endpoint interface.
func (e *endpointsInfo) Equal(other proxy.Endpoint) bool {
	o, ok := other.(*endpointsInfo)
	if !ok {
		glog.Errorf("Failed to cast endpointsInfo")
		return false
	}
	return e.endpoint == o.endpoint &&
		e.isLocal == o.isLocal &&
		e.protocol == o.protocol &&
		e.chainName == o.chainName
}

// String is part of proxy.Endpoint interface.
func (e *endpointsInfo) String() string {
	return e.endpoint
}

// Returns the endpoint chain name for a given endpointsInfo.
func (e *endpointsInfo) endpointChain(svcNameString, protocol string) utiliptables.Chain {
	if e.protocol != protocol {
		e.protocol = protocol
		e.chainName = servicePortEndpointChainName(svcNameString, protocol, e.endpoint)
	}
	return e.chainName
}

var _ proxy.Endpoint = &endpointsInfo{}

// Proxier is an iptables based proxy for connections between a localhost:lport
// and services that provide the actual backends.
type Proxier struct {
	// endpointsChanges and serviceChanges contains all changes to endpoints and
	// services that happened since iptables was synced. For a single object,
	// changes are accumulated, i.e. previous is state from before all of them,
	// current is state after applying all of those.
	endpointsChanges *proxy.EndpointChangeTracker
	serviceChanges   *proxy.ServiceChangeTracker

	mu           sync.Mutex // protects the following fields
	serviceMap   proxy.ServiceMap
	endpointsMap proxy.EndpointsMap
	portsMap     map[utilproxy.LocalPort]utilproxy.Closeable
	// endpointsSynced and servicesSynced are set to true when corresponding
	// objects are synced after startup. This is used to avoid updating iptables
	// with some partial data after kube-proxy restart.
	endpointsSynced bool
	servicesSynced  bool
	initialized     int32
	syncRunner      *async.BoundedFrequencyRunner // governs calls to syncProxyRules

	// These are effectively const and do not need the mutex to be held.
	iptables       utiliptables.Interface
	masqueradeAll  bool
	masqueradeMark string
	exec           utilexec.Interface
	clusterCIDR    string
	hostname       string
	nodeIP         net.IP
	portMapper     utilproxy.PortOpener
	recorder       record.EventRecorder
	healthChecker  healthcheck.Server
	healthzServer  healthcheck.HealthzUpdater

	// Since converting probabilities (floats) to strings is expensive
	// and we are using only probabilities in the format of 1/n, we are
	// precomputing some number of those and cache for future reuse.
	precomputedProbabilities []string

	// The following buffers are used to reuse memory and avoid allocations
	// that are significantly impacting performance.
	iptablesData *bytes.Buffer
	filterChains *bytes.Buffer
	filterRules  *bytes.Buffer
	natChains    *bytes.Buffer
	natRules     *bytes.Buffer
}

// listenPortOpener opens ports by calling bind() and listen().
type listenPortOpener struct{}

// OpenLocalPort holds the given local port open.
func (l *listenPortOpener) OpenLocalPort(lp *utilproxy.LocalPort) (utilproxy.Closeable, error) {
	return openLocalPort(lp)
}

// Proxier implements ProxyProvider
var _ proxy.ProxyProvider = &Proxier{}

// NewProxier returns a new Proxier given an iptables Interface instance.
// Because of the iptables logic, it is assumed that there is only a single Proxier active on a machine.
// An error will be returned if iptables fails to update or acquire the initial lock.
// Once a proxier is created, it will keep iptables up to date in the background and
// will not terminate if a particular iptables call fails.
func NewProxier(ipt utiliptables.Interface,
	sysctl utilsysctl.Interface,
	exec utilexec.Interface,
	syncPeriod time.Duration,
	minSyncPeriod time.Duration,
	masqueradeAll bool,
	masqueradeBit int,
	clusterCIDR string,
	hostname string,
	nodeIP net.IP,
	recorder record.EventRecorder,
	healthzServer healthcheck.HealthzUpdater,
) (*Proxier, error) {
	// Set the route_localnet sysctl we need for
	if err := sysctl.SetSysctl(sysctlRouteLocalnet, 1); err != nil {
		return nil, fmt.Errorf("can't set sysctl %s: %v", sysctlRouteLocalnet, err)
	}

	// Proxy needs br_netfilter and bridge-nf-call-iptables=1 when containers
	// are connected to a Linux bridge (but not SDN bridges).  Until most
	// plugins handle this, log when config is missing
	if val, err := sysctl.GetSysctl(sysctlBridgeCallIPTables); err == nil && val != 1 {
		glog.Warningf("missing br-netfilter module or unset sysctl br-nf-call-iptables; proxy may not work as intended")
	}

	// Generate the masquerade mark to use for SNAT rules.
	masqueradeValue := 1 << uint(masqueradeBit)
	masqueradeMark := fmt.Sprintf("%#08x/%#08x", masqueradeValue, masqueradeValue)

	if nodeIP == nil {
		glog.Warningf("invalid nodeIP, initializing kube-proxy with 127.0.0.1 as nodeIP")
		nodeIP = net.ParseIP("127.0.0.1")
	}

	if len(clusterCIDR) == 0 {
		glog.Warningf("clusterCIDR not specified, unable to distinguish between internal and external traffic")
	}

	healthChecker := healthcheck.NewServer(hostname, recorder, nil, nil) // use default implementations of deps

	proxier := &Proxier{
		portsMap:                 make(map[utilproxy.LocalPort]utilproxy.Closeable),
		serviceMap:               make(proxy.ServiceMap),
		serviceChanges:           proxy.NewServiceChangeTracker(),
		endpointsMap:             make(proxy.EndpointsMap),
		endpointsChanges:         proxy.NewEndpointChangeTracker(hostname),
		iptables:                 ipt,
		masqueradeAll:            masqueradeAll,
		masqueradeMark:           masqueradeMark,
		exec:                     exec,
		clusterCIDR:              clusterCIDR,
		hostname:                 hostname,
		nodeIP:                   nodeIP,
		portMapper:               &listenPortOpener{},
		recorder:                 recorder,
		healthChecker:            healthChecker,
		healthzServer:            healthzServer,
		precomputedProbabilities: make([]string, 0, 1001),
		iptablesData:             bytes.NewBuffer(nil),
		filterChains:             bytes.NewBuffer(nil),
		filterRules:              bytes.NewBuffer(nil),
		natChains:                bytes.NewBuffer(nil),
		natRules:                 bytes.NewBuffer(nil),
	}
	burstSyncs := 2
	glog.V(3).Infof("minSyncPeriod: %v, syncPeriod: %v, burstSyncs: %d", minSyncPeriod, syncPeriod, burstSyncs)
	proxier.syncRunner = async.NewBoundedFrequencyRunner("sync-runner", proxier.syncProxyRules, minSyncPeriod, syncPeriod, burstSyncs)
	return proxier, nil
}

type iptablesJumpChain struct {
	table       utiliptables.Table
	chain       utiliptables.Chain
	sourceChain utiliptables.Chain
	comment     string
}

var iptablesJumpChains = []iptablesJumpChain{
	{utiliptables.TableFilter, kubeServicesChain, utiliptables.ChainInput, "kubernetes service portals"},
	{utiliptables.TableFilter, kubeServicesChain, utiliptables.ChainOutput, "kubernetes service portals"},
	{utiliptables.TableNAT, kubeServicesChain, utiliptables.ChainOutput, "kubernetes service portals"},
	{utiliptables.TableNAT, kubeServicesChain, utiliptables.ChainPrerouting, "kubernetes service portals"},
	{utiliptables.TableNAT, kubePostroutingChain, utiliptables.ChainPostrouting, "kubernetes postrouting rules"},
	{utiliptables.TableFilter, kubeForwardChain, utiliptables.ChainForward, "kubernetes forwarding rules"},
}

// CleanupLeftovers removes all iptables rules and chains created by the Proxier
// It returns true if an error was encountered. Errors are logged.
func CleanupLeftovers(ipt utiliptables.Interface) (encounteredError bool) {
	// Unlink our chains
	for _, chain := range iptablesJumpChains {
		args := []string{
			"-m", "comment", "--comment", chain.comment,
			"-j", string(chain.chain),
		}
		if err := ipt.DeleteRule(chain.table, chain.sourceChain, args...); err != nil {
			if !utiliptables.IsNotFoundError(err) {
				glog.Errorf("Error removing pure-iptables proxy rule: %v", err)
				encounteredError = true
			}
		}
	}

	// Flush and remove all of our "-t nat" chains.
	iptablesData := bytes.NewBuffer(nil)
	if err := ipt.SaveInto(utiliptables.TableNAT, iptablesData); err != nil {
		glog.Errorf("Failed to execute iptables-save for %s: %v", utiliptables.TableNAT, err)
		encounteredError = true
	} else {
		existingNATChains := utiliptables.GetChainLines(utiliptables.TableNAT, iptablesData.Bytes())
		natChains := bytes.NewBuffer(nil)
		natRules := bytes.NewBuffer(nil)
		writeLine(natChains, "*nat")
		// Start with chains we know we need to remove.
		for _, chain := range []utiliptables.Chain{kubeServicesChain, kubeNodePortsChain, kubePostroutingChain, KubeMarkMasqChain} {
			if _, found := existingNATChains[chain]; found {
				chainString := string(chain)
				writeLine(natChains, existingNATChains[chain]) // flush
				writeLine(natRules, "-X", chainString)         // delete
			}
		}
		// Hunt for service and endpoint chains.
		for chain := range existingNATChains {
			chainString := string(chain)
			if strings.HasPrefix(chainString, "KUBE-SVC-") || strings.HasPrefix(chainString, "KUBE-SEP-") || strings.HasPrefix(chainString, "KUBE-FW-") || strings.HasPrefix(chainString, "KUBE-XLB-") {
				writeLine(natChains, existingNATChains[chain]) // flush
				writeLine(natRules, "-X", chainString)         // delete
			}
		}
		writeLine(natRules, "COMMIT")
		natLines := append(natChains.Bytes(), natRules.Bytes()...)
		// Write it.
		err = ipt.Restore(utiliptables.TableNAT, natLines, utiliptables.NoFlushTables, utiliptables.RestoreCounters)
		if err != nil {
			glog.Errorf("Failed to execute iptables-restore for %s: %v", utiliptables.TableNAT, err)
			encounteredError = true
		}
	}

	// Flush and remove all of our "-t filter" chains.
	iptablesData = bytes.NewBuffer(nil)
	if err := ipt.SaveInto(utiliptables.TableFilter, iptablesData); err != nil {
		glog.Errorf("Failed to execute iptables-save for %s: %v", utiliptables.TableFilter, err)
		encounteredError = true
	} else {
		existingFilterChains := utiliptables.GetChainLines(utiliptables.TableFilter, iptablesData.Bytes())
		filterChains := bytes.NewBuffer(nil)
		filterRules := bytes.NewBuffer(nil)
		writeLine(filterChains, "*filter")
		for _, chain := range []utiliptables.Chain{kubeServicesChain, kubeForwardChain} {
			if _, found := existingFilterChains[chain]; found {
				chainString := string(chain)
				writeLine(filterChains, existingFilterChains[chain])
				writeLine(filterRules, "-X", chainString)
			}
		}
		writeLine(filterRules, "COMMIT")
		filterLines := append(filterChains.Bytes(), filterRules.Bytes()...)
		// Write it.
		if err := ipt.Restore(utiliptables.TableFilter, filterLines, utiliptables.NoFlushTables, utiliptables.RestoreCounters); err != nil {
			glog.Errorf("Failed to execute iptables-restore for %s: %v", utiliptables.TableFilter, err)
			encounteredError = true
		}
	}
	return encounteredError
}

func computeProbability(n int) string {
	return fmt.Sprintf("%0.5f", 1.0/float64(n))
}

// This assumes proxier.mu is held
func (proxier *Proxier) precomputeProbabilities(numberOfPrecomputed int) {
	if len(proxier.precomputedProbabilities) == 0 {
		proxier.precomputedProbabilities = append(proxier.precomputedProbabilities, "<bad value>")
	}
	for i := len(proxier.precomputedProbabilities); i <= numberOfPrecomputed; i++ {
		proxier.precomputedProbabilities = append(proxier.precomputedProbabilities, computeProbability(i))
	}
}

// This assumes proxier.mu is held
func (proxier *Proxier) probability(n int) string {
	if n >= len(proxier.precomputedProbabilities) {
		proxier.precomputeProbabilities(n)
	}
	return proxier.precomputedProbabilities[n]
}

// Sync is called to synchronize the proxier state to iptables as soon as possible.
func (proxier *Proxier) Sync() {
	proxier.syncRunner.Run()
}

// SyncLoop runs periodic work.  This is expected to run as a goroutine or as the main loop of the app.  It does not return.
func (proxier *Proxier) SyncLoop() {
	// Update healthz timestamp at beginning in case Sync() never succeeds.
	if proxier.healthzServer != nil {
		proxier.healthzServer.UpdateTimestamp()
	}
	proxier.syncRunner.Loop(wait.NeverStop)
}

func (proxier *Proxier) setInitialized(value bool) {
	var initialized int32
	if value {
		initialized = 1
	}
	atomic.StoreInt32(&proxier.initialized, initialized)
}

func (proxier *Proxier) isInitialized() bool {
	return atomic.LoadInt32(&proxier.initialized) > 0
}

func (proxier *Proxier) OnServiceAdd(service *api.Service) {
	if proxier.serviceChanges.Update(nil, service, newServiceInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnServiceUpdate(oldService, service *api.Service) {
	if proxier.serviceChanges.Update(oldService, service, newServiceInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnServiceDelete(service *api.Service) {
	if proxier.serviceChanges.Update(service, nil, newServiceInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnServiceSynced() {
	proxier.mu.Lock()
	proxier.servicesSynced = true
	proxier.setInitialized(proxier.servicesSynced && proxier.endpointsSynced)
	proxier.mu.Unlock()

	// Sync unconditionally - this is called once per lifetime.
	proxier.syncProxyRules()
}

func (proxier *Proxier) OnEndpointsAdd(endpoints *api.Endpoints) {
	if proxier.endpointsChanges.Update(nil, endpoints, newEndpointsInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnEndpointsUpdate(oldEndpoints, endpoints *api.Endpoints) {
	if proxier.endpointsChanges.Update(oldEndpoints, endpoints, newEndpointsInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnEndpointsDelete(endpoints *api.Endpoints) {
	if proxier.endpointsChanges.Update(endpoints, nil, newEndpointsInfo) && proxier.isInitialized() {
		proxier.syncRunner.Run()
	}
}

func (proxier *Proxier) OnEndpointsSynced() {
	proxier.mu.Lock()
	proxier.endpointsSynced = true
	proxier.setInitialized(proxier.servicesSynced && proxier.endpointsSynced)
	proxier.mu.Unlock()

	// Sync unconditionally - this is called once per lifetime.
	proxier.syncProxyRules()
}

// portProtoHash takes the ServicePortName and protocol for a service
// returns the associated 16 character hash. This is computed by hashing (sha256)
// then encoding to base32 and truncating to 16 chars. We do this because IPTables
// Chain Names must be <= 28 chars long, and the longer they are the harder they are to read.
func portProtoHash(servicePortName string, protocol string) string {
	hash := sha256.Sum256([]byte(servicePortName + protocol))
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	return encoded[:16]
}

// servicePortChainName takes the ServicePortName for a service and
// returns the associated iptables chain.  This is computed by hashing (sha256)
// then encoding to base32 and truncating with the prefix "KUBE-SVC-".
func servicePortChainName(servicePortName string, protocol string) utiliptables.Chain {
	return utiliptables.Chain("KUBE-SVC-" + portProtoHash(servicePortName, protocol))
}

// serviceFirewallChainName takes the ServicePortName for a service and
// returns the associated iptables chain.  This is computed by hashing (sha256)
// then encoding to base32 and truncating with the prefix "KUBE-FW-".
func serviceFirewallChainName(servicePortName string, protocol string) utiliptables.Chain {
	return utiliptables.Chain("KUBE-FW-" + portProtoHash(servicePortName, protocol))
}

// serviceLBPortChainName takes the ServicePortName for a service and
// returns the associated iptables chain.  This is computed by hashing (sha256)
// then encoding to base32 and truncating with the prefix "KUBE-XLB-".  We do
// this because IPTables Chain Names must be <= 28 chars long, and the longer
// they are the harder they are to read.
func serviceLBChainName(servicePortName string, protocol string) utiliptables.Chain {
	return utiliptables.Chain("KUBE-XLB-" + portProtoHash(servicePortName, protocol))
}

// This is the same as servicePortChainName but with the endpoint included.
func servicePortEndpointChainName(servicePortName string, protocol string, endpoint string) utiliptables.Chain {
	hash := sha256.Sum256([]byte(servicePortName + protocol + endpoint))
	encoded := base32.StdEncoding.EncodeToString(hash[:])
	return utiliptables.Chain("KUBE-SEP-" + encoded[:16])
}

// After a UDP endpoint has been removed, we must flush any pending conntrack entries to it, or else we
// risk sending more traffic to it, all of which will be lost (because UDP).
// This assumes the proxier mutex is held
// TODO: move it to util
func (proxier *Proxier) deleteEndpointConnections(connectionMap []proxy.ServiceEndpoint) {
	for _, epSvcPair := range connectionMap {
		if svcInfo, ok := proxier.serviceMap[epSvcPair.ServicePortName]; ok && svcInfo.Protocol() == api.ProtocolUDP {
			endpointIP := utilproxy.IPPart(epSvcPair.Endpoint)
			err := utilproxy.ClearUDPConntrackForPeers(proxier.exec, svcInfo.ClusterIP(), endpointIP)
			if err != nil {
				glog.Errorf("Failed to delete %s endpoint connections, error: %v", epSvcPair.ServicePortName.String(), err)
			}
		}
	}
}

// This is where all of the iptables-save/restore calls happen.
// The only other iptables rules are those that are setup in iptablesInit()
// This assumes proxier.mu is NOT held
func (proxier *Proxier) syncProxyRules() {
	proxier.mu.Lock()
	defer proxier.mu.Unlock()

	start := time.Now()
	defer func() {
		metrics.SyncProxyRulesLatency.Observe(metrics.SinceInMicroseconds(start))
		glog.V(4).Infof("syncProxyRules took %v", time.Since(start))
	}()
	// don't sync rules till we've received services and endpoints
	if !proxier.endpointsSynced || !proxier.servicesSynced {
		glog.V(2).Info("Not syncing iptables until Services and Endpoints have been received from master")
		return
	}

	// We assume that if this was called, we really want to sync them,
	// even if nothing changed in the meantime. In other words, callers are
	// responsible for detecting no-op changes and not calling this function.
	serviceUpdateResult := proxy.UpdateServiceMap(proxier.serviceMap, proxier.serviceChanges)
	endpointUpdateResult := proxy.UpdateEndpointsMap(proxier.endpointsMap, proxier.endpointsChanges)

	staleServices := serviceUpdateResult.UDPStaleClusterIP
	// merge stale services gathered from updateEndpointsMap
	for _, svcPortName := range endpointUpdateResult.StaleServiceNames {
		if svcInfo, ok := proxier.serviceMap[svcPortName]; ok && svcInfo != nil && svcInfo.Protocol() == api.ProtocolUDP {
			glog.V(2).Infof("Stale udp service %v -> %s", svcPortName, svcInfo.ClusterIP())
			staleServices.Insert(svcInfo.ClusterIP())
		}
	}

	glog.V(3).Infof("Syncing iptables rules")

	// Create and link the kube chains.
	for _, chain := range iptablesJumpChains {
		if _, err := proxier.iptables.EnsureChain(chain.table, chain.chain); err != nil {
			glog.Errorf("Failed to ensure that %s chain %s exists: %v", chain.table, kubeServicesChain, err)
			return
		}
		args := []string{
			"-m", "comment", "--comment", chain.comment,
			"-j", string(chain.chain),
		}
		if _, err := proxier.iptables.EnsureRule(utiliptables.Prepend, chain.table, chain.sourceChain, args...); err != nil {
			glog.Errorf("Failed to ensure that %s chain %s jumps to %s: %v", chain.table, chain.sourceChain, chain.chain, err)
			return
		}
	}

	//
	// Below this point we will not return until we try to write the iptables rules.
	//

	// Get iptables-save output so we can check for existing chains and rules.
	// This will be a map of chain name to chain with rules as stored in iptables-save/iptables-restore
	existingFilterChains := make(map[utiliptables.Chain]string)
	proxier.iptablesData.Reset()
	err := proxier.iptables.SaveInto(utiliptables.TableFilter, proxier.iptablesData)
	if err != nil { // if we failed to get any rules
		glog.Errorf("Failed to execute iptables-save, syncing all rules: %v", err)
	} else { // otherwise parse the output
		existingFilterChains = utiliptables.GetChainLines(utiliptables.TableFilter, proxier.iptablesData.Bytes())
	}

	existingNATChains := make(map[utiliptables.Chain]string)
	proxier.iptablesData.Reset()
	err = proxier.iptables.SaveInto(utiliptables.TableNAT, proxier.iptablesData)
	if err != nil { // if we failed to get any rules
		glog.Errorf("Failed to execute iptables-save, syncing all rules: %v", err)
	} else { // otherwise parse the output
		existingNATChains = utiliptables.GetChainLines(utiliptables.TableNAT, proxier.iptablesData.Bytes())
	}

	// Reset all buffers used later.
	// This is to avoid memory reallocations and thus improve performance.
	proxier.filterChains.Reset()
	proxier.filterRules.Reset()
	proxier.natChains.Reset()
	proxier.natRules.Reset()

	// Write table headers.
	writeLine(proxier.filterChains, "*filter")
	writeLine(proxier.natChains, "*nat")

	// Make sure we keep stats for the top-level chains, if they existed
	// (which most should have because we created them above).
	for _, chainName := range []utiliptables.Chain{kubeServicesChain, kubeForwardChain} {
		if chain, ok := existingFilterChains[chainName]; ok {
			writeLine(proxier.filterChains, chain)
		} else {
			writeLine(proxier.filterChains, utiliptables.MakeChainLine(chainName))
		}
	}
	for _, chainName := range []utiliptables.Chain{kubeServicesChain, kubeNodePortsChain, kubePostroutingChain, KubeMarkMasqChain} {
		if chain, ok := existingNATChains[chainName]; ok {
			writeLine(proxier.natChains, chain)
		} else {
			writeLine(proxier.natChains, utiliptables.MakeChainLine(chainName))
		}
	}

	// Install the kubernetes-specific postrouting rules. We use a whole chain for
	// this so that it is easier to flush and change, for example if the mark
	// value should ever change.
	writeLine(proxier.natRules, []string{
		"-A", string(kubePostroutingChain),
		"-m", "comment", "--comment", `"kubernetes service traffic requiring SNAT"`,
		"-m", "mark", "--mark", proxier.masqueradeMark,
		"-j", "MASQUERADE",
	}...)

	// Install the kubernetes-specific masquerade mark rule. We use a whole chain for
	// this so that it is easier to flush and change, for example if the mark
	// value should ever change.
	writeLine(proxier.natRules, []string{
		"-A", string(KubeMarkMasqChain),
		"-j", "MARK", "--set-xmark", proxier.masqueradeMark,
	}...)

	// Accumulate NAT chains to keep.
	activeNATChains := map[utiliptables.Chain]bool{} // use a map as a set

	// Accumulate the set of local ports that we will be holding open once this update is complete
	replacementPortsMap := map[utilproxy.LocalPort]utilproxy.Closeable{}

	// We are creating those slices ones here to avoid memory reallocations
	// in every loop. Note that reuse the memory, instead of doing:
	//   slice = <some new slice>
	// you should always do one of the below:
	//   slice = slice[:0] // and then append to it
	//   slice = append(slice[:0], ...)
	endpoints := make([]*endpointsInfo, 0)
	endpointChains := make([]utiliptables.Chain, 0)
	// To avoid growing this slice, we arbitrarily set its size to 64,
	// there is never more than that many arguments for a single line.
	// Note that even if we go over 64, it will still be correct - it
	// is just for efficiency, not correctness.
	args := make([]string, 64)

	// Build rules for each service.
	var svcNameString string
	for svcName, svc := range proxier.serviceMap {
		svcInfo, ok := svc.(*serviceInfo)
		if !ok {
			glog.Errorf("Failed to cast serviceInfo %q", svcName.String())
			continue
		}
		isIPv6 := utilproxy.IsIPv6(svcInfo.clusterIP)
		protocol := strings.ToLower(string(svcInfo.protocol))
		svcNameString = svcInfo.serviceNameString

		// Create the per-service chain, retaining counters if possible.
		svcChain := svcInfo.servicePortChainName
		if chain, ok := existingNATChains[svcChain]; ok {
			writeLine(proxier.natChains, chain)
		} else {
			writeLine(proxier.natChains, utiliptables.MakeChainLine(svcChain))
		}
		activeNATChains[svcChain] = true

		svcXlbChain := svcInfo.serviceLBChainName
		if svcInfo.onlyNodeLocalEndpoints {
			// Only for services request OnlyLocal traffic
			// create the per-service LB chain, retaining counters if possible.
			if lbChain, ok := existingNATChains[svcXlbChain]; ok {
				writeLine(proxier.natChains, lbChain)
			} else {
				writeLine(proxier.natChains, utiliptables.MakeChainLine(svcXlbChain))
			}
			activeNATChains[svcXlbChain] = true
		} else if activeNATChains[svcXlbChain] {
			// Cleanup the previously created XLB chain for this service
			delete(activeNATChains, svcXlbChain)
		}

		// Capture the clusterIP.
		args = append(args[:0],
			"-A", string(kubeServicesChain),
			"-m", "comment", "--comment", fmt.Sprintf(`"%s cluster IP"`, svcNameString),
			"-m", protocol, "-p", protocol,
			"-d", utilproxy.ToCIDR(svcInfo.clusterIP),
			"--dport", strconv.Itoa(svcInfo.port),
		)
		if proxier.masqueradeAll {
			writeLine(proxier.natRules, append(args, "-j", string(KubeMarkMasqChain))...)
		} else if len(proxier.clusterCIDR) > 0 {
			// This masquerades off-cluster traffic to a service VIP.  The idea
			// is that you can establish a static route for your Service range,
			// routing to any node, and that node will bridge into the Service
			// for you.  Since that might bounce off-node, we masquerade here.
			// If/when we support "Local" policy for VIPs, we should update this.
			writeLine(proxier.natRules, append(args, "! -s", proxier.clusterCIDR, "-j", string(KubeMarkMasqChain))...)
		}
		writeLine(proxier.natRules, append(args, "-j", string(svcChain))...)

		// Capture externalIPs.
		for _, externalIP := range svcInfo.externalIPs {
			// If the "external" IP happens to be an IP that is local to this
			// machine, hold the local port open so no other process can open it
			// (because the socket might open but it would never work).
			if local, err := utilproxy.IsLocalIP(externalIP); err != nil {
				glog.Errorf("can't determine if IP is local, assuming not: %v", err)
			} else if local {
				lp := utilproxy.LocalPort{
					Description: "externalIP for " + svcNameString,
					IP:          externalIP,
					Port:        svcInfo.Port(),
					Protocol:    protocol,
				}
				if proxier.portsMap[lp] != nil {
					glog.V(4).Infof("Port %s was open before and is still needed", lp.String())
					replacementPortsMap[lp] = proxier.portsMap[lp]
				} else {
					socket, err := proxier.portMapper.OpenLocalPort(&lp)
					if err != nil {
						msg := fmt.Sprintf("can't open %s, skipping this externalIP: %v", lp.String(), err)

						proxier.recorder.Eventf(
							&v1.ObjectReference{
								Kind:      "Node",
								Name:      proxier.hostname,
								UID:       types.UID(proxier.hostname),
								Namespace: "",
							}, api.EventTypeWarning, err.Error(), msg)
						glog.Error(msg)
						continue
					}
					replacementPortsMap[lp] = socket
				}
			} // We're holding the port, so it's OK to install iptables rules.
			args = append(args[:0],
				"-A", string(kubeServicesChain),
				"-m", "comment", "--comment", fmt.Sprintf(`"%s external IP"`, svcNameString),
				"-m", protocol, "-p", protocol,
				"-d", utilproxy.ToCIDR(net.ParseIP(externalIP)),
				"--dport", strconv.Itoa(svcInfo.port),
			)
			// We have to SNAT packets to external IPs.
			writeLine(proxier.natRules, append(args, "-j", string(KubeMarkMasqChain))...)

			// Allow traffic for external IPs that does not come from a bridge (i.e. not from a container)
			// nor from a local process to be forwarded to the service.
			// This rule roughly translates to "all traffic from off-machine".
			// This is imperfect in the face of network plugins that might not use a bridge, but we can revisit that later.
			externalTrafficOnlyArgs := append(args,
				"-m", "physdev", "!", "--physdev-is-in",
				"-m", "addrtype", "!", "--src-type", "LOCAL")
			writeLine(proxier.natRules, append(externalTrafficOnlyArgs, "-j", string(svcChain))...)
			dstLocalOnlyArgs := append(args, "-m", "addrtype", "--dst-type", "LOCAL")
			// Allow traffic bound for external IPs that happen to be recognized as local IPs to stay local.
			// This covers cases like GCE load-balancers which get added to the local routing table.
			writeLine(proxier.natRules, append(dstLocalOnlyArgs, "-j", string(svcChain))...)

			// If the service has no endpoints then reject packets coming via externalIP
			// Install ICMP Reject rule in filter table for destination=externalIP and dport=svcport
			if len(proxier.endpointsMap[svcName]) == 0 {
				writeLine(proxier.filterRules,
					"-A", string(kubeServicesChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s has no endpoints"`, svcNameString),
					"-m", protocol, "-p", protocol,
					"-d", utilproxy.ToCIDR(net.ParseIP(externalIP)),
					"--dport", strconv.Itoa(svcInfo.port),
					"-j", "REJECT",
				)
			}
		}

		// Capture load-balancer ingress.
		fwChain := svcInfo.serviceFirewallChainName
		for _, ingress := range svcInfo.loadBalancerStatus.Ingress {
			if ingress.IP != "" {
				// create service firewall chain
				if chain, ok := existingNATChains[fwChain]; ok {
					writeLine(proxier.natChains, chain)
				} else {
					writeLine(proxier.natChains, utiliptables.MakeChainLine(fwChain))
				}
				activeNATChains[fwChain] = true
				// The service firewall rules are created based on ServiceSpec.loadBalancerSourceRanges field.
				// This currently works for loadbalancers that preserves source ips.
				// For loadbalancers which direct traffic to service NodePort, the firewall rules will not apply.

				args = append(args[:0],
					"-A", string(kubeServicesChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s loadbalancer IP"`, svcNameString),
					"-m", protocol, "-p", protocol,
					"-d", utilproxy.ToCIDR(net.ParseIP(ingress.IP)),
					"--dport", strconv.Itoa(svcInfo.port),
				)
				// jump to service firewall chain
				writeLine(proxier.natRules, append(args, "-j", string(fwChain))...)

				args = append(args[:0],
					"-A", string(fwChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s loadbalancer IP"`, svcNameString),
				)

				// Each source match rule in the FW chain may jump to either the SVC or the XLB chain
				chosenChain := svcXlbChain
				// If we are proxying globally, we need to masquerade in case we cross nodes.
				// If we are proxying only locally, we can retain the source IP.
				if !svcInfo.onlyNodeLocalEndpoints {
					writeLine(proxier.natRules, append(args, "-j", string(KubeMarkMasqChain))...)
					chosenChain = svcChain
				}

				if len(svcInfo.loadBalancerSourceRanges) == 0 {
					// allow all sources, so jump directly to the KUBE-SVC or KUBE-XLB chain
					writeLine(proxier.natRules, append(args, "-j", string(chosenChain))...)
				} else {
					// firewall filter based on each source range
					allowFromNode := false
					for _, src := range svcInfo.loadBalancerSourceRanges {
						writeLine(proxier.natRules, append(args, "-s", src, "-j", string(chosenChain))...)
						// ignore error because it has been validated
						_, cidr, _ := net.ParseCIDR(src)
						if cidr.Contains(proxier.nodeIP) {
							allowFromNode = true
						}
					}
					// generally, ip route rule was added to intercept request to loadbalancer vip from the
					// loadbalancer's backend hosts. In this case, request will not hit the loadbalancer but loop back directly.
					// Need to add the following rule to allow request on host.
					if allowFromNode {
						writeLine(proxier.natRules, append(args, "-s", utilproxy.ToCIDR(net.ParseIP(ingress.IP)), "-j", string(chosenChain))...)
					}
				}

				// If the packet was able to reach the end of firewall chain, then it did not get DNATed.
				// It means the packet cannot go thru the firewall, then mark it for DROP
				writeLine(proxier.natRules, append(args, "-j", string(KubeMarkDropChain))...)
			}
		}

		// Capture nodeports.  If we had more than 2 rules it might be
		// worthwhile to make a new per-service chain for nodeport rules, but
		// with just 2 rules it ends up being a waste and a cognitive burden.
		if svcInfo.nodePort != 0 {
			// Hold the local port open so no other process can open it
			// (because the socket might open but it would never work).
			lp := utilproxy.LocalPort{
				Description: "nodePort for " + svcNameString,
				IP:          "",
				Port:        svcInfo.nodePort,
				Protocol:    protocol,
			}
			if proxier.portsMap[lp] != nil {
				glog.V(4).Infof("Port %s was open before and is still needed", lp.String())
				replacementPortsMap[lp] = proxier.portsMap[lp]
			} else {
				socket, err := proxier.portMapper.OpenLocalPort(&lp)
				if err != nil {
					glog.Errorf("can't open %s, skipping this nodePort: %v", lp.String(), err)
					continue
				}
				if lp.Protocol == "udp" {
					// TODO: We might have multiple services using the same port, and this will clear conntrack for all of them.
					// This is very low impact. The NodePort range is intentionally obscure, and unlikely to actually collide with real Services.
					// This only affects UDP connections, which are not common.
					// See issue: https://github.com/kubernetes/kubernetes/issues/49881
					err := utilproxy.ClearUDPConntrackForPort(proxier.exec, lp.Port, isIPv6)
					if err != nil {
						glog.Errorf("Failed to clear udp conntrack for port %d, error: %v", lp.Port, err)
					}
				}
				replacementPortsMap[lp] = socket
			} // We're holding the port, so it's OK to install iptables rules.

			args = append(args[:0],
				"-A", string(kubeNodePortsChain),
				"-m", "comment", "--comment", svcNameString,
				"-m", protocol, "-p", protocol,
				"--dport", strconv.Itoa(svcInfo.nodePort),
			)
			if !svcInfo.onlyNodeLocalEndpoints {
				// Nodeports need SNAT, unless they're local.
				writeLine(proxier.natRules, append(args, "-j", string(KubeMarkMasqChain))...)
				// Jump to the service chain.
				writeLine(proxier.natRules, append(args, "-j", string(svcChain))...)
			} else {
				// TODO: Make all nodePorts jump to the firewall chain.
				// Currently we only create it for loadbalancers (#33586).

				// Fix localhost martian source error
				loopback := "127.0.0.0/8"
				if isIPv6 {
					loopback = "::1/128"
				}
				writeLine(proxier.natRules, append(args, "-s", loopback, "-j", string(KubeMarkMasqChain))...)
				writeLine(proxier.natRules, append(args, "-j", string(svcXlbChain))...)
			}

			// If the service has no endpoints then reject packets.  The filter
			// table doesn't currently have the same per-service structure that
			// the nat table does, so we just stick this into the kube-services
			// chain.
			if len(proxier.endpointsMap[svcName]) == 0 {
				writeLine(proxier.filterRules,
					"-A", string(kubeServicesChain),
					"-m", "comment", "--comment", fmt.Sprintf(`"%s has no endpoints"`, svcNameString),
					"-m", "addrtype", "--dst-type", "LOCAL",
					"-m", protocol, "-p", protocol,
					"--dport", strconv.Itoa(svcInfo.nodePort),
					"-j", "REJECT",
				)
			}
		}

		// If the service has no endpoints then reject packets.
		if len(proxier.endpointsMap[svcName]) == 0 {
			writeLine(proxier.filterRules,
				"-A", string(kubeServicesChain),
				"-m", "comment", "--comment", fmt.Sprintf(`"%s has no endpoints"`, svcNameString),
				"-m", protocol, "-p", protocol,
				"-d", utilproxy.ToCIDR(svcInfo.clusterIP),
				"--dport", strconv.Itoa(svcInfo.port),
				"-j", "REJECT",
			)
			continue
		}

		// From here on, we assume there are active endpoints.

		// Generate the per-endpoint chains.  We do this in multiple passes so we
		// can group rules together.
		// These two slices parallel each other - keep in sync
		endpoints = endpoints[:0]
		endpointChains = endpointChains[:0]
		var endpointChain utiliptables.Chain
		for _, ep := range proxier.endpointsMap[svcName] {
			epInfo, ok := ep.(*endpointsInfo)
			if !ok {
				glog.Errorf("Failed to cast endpointsInfo %q", ep.String())
				continue
			}
			endpoints = append(endpoints, epInfo)
			endpointChain = epInfo.endpointChain(svcNameString, protocol)
			endpointChains = append(endpointChains, endpointChain)

			// Create the endpoint chain, retaining counters if possible.
			if chain, ok := existingNATChains[utiliptables.Chain(endpointChain)]; ok {
				writeLine(proxier.natChains, chain)
			} else {
				writeLine(proxier.natChains, utiliptables.MakeChainLine(endpointChain))
			}
			activeNATChains[endpointChain] = true
		}

		// First write session affinity rules, if applicable.
		if svcInfo.sessionAffinityType == api.ServiceAffinityClientIP {
			for _, endpointChain := range endpointChains {
				writeLine(proxier.natRules,
					"-A", string(svcChain),
					"-m", "comment", "--comment", svcNameString,
					"-m", "recent", "--name", string(endpointChain),
					"--rcheck", "--seconds", strconv.Itoa(svcInfo.stickyMaxAgeSeconds), "--reap",
					"-j", string(endpointChain))
			}
		}

		// Now write loadbalancing & DNAT rules.
		n := len(endpointChains)
		for i, endpointChain := range endpointChains {
			epIP := endpoints[i].IP()
			if epIP == "" {
				// Error parsing this endpoint has been logged. Skip to next endpoint.
				continue
			}
			// Balancing rules in the per-service chain.
			args = append(args[:0], []string{
				"-A", string(svcChain),
				"-m", "comment", "--comment", svcNameString,
			}...)
			if i < (n - 1) {
				// Each rule is a probabilistic match.
				args = append(args,
					"-m", "statistic",
					"--mode", "random",
					"--probability", proxier.probability(n-i))
			}
			// The final (or only if n == 1) rule is a guaranteed match.
			args = append(args, "-j", string(endpointChain))
			writeLine(proxier.natRules, args...)

			// Rules in the per-endpoint chain.
			args = append(args[:0],
				"-A", string(endpointChain),
				"-m", "comment", "--comment", svcNameString,
			)
			// Handle traffic that loops back to the originator with SNAT.
			writeLine(proxier.natRules, append(args,
				"-s", utilproxy.ToCIDR(net.ParseIP(epIP)),
				"-j", string(KubeMarkMasqChain))...)
			// Update client-affinity lists.
			if svcInfo.sessionAffinityType == api.ServiceAffinityClientIP {
				args = append(args, "-m", "recent", "--name", string(endpointChain), "--set")
			}
			// DNAT to final destination.
			args = append(args, "-m", protocol, "-p", protocol, "-j", "DNAT", "--to-destination", endpoints[i].endpoint)
			writeLine(proxier.natRules, args...)
		}

		// The logic below this applies only if this service is marked as OnlyLocal
		if !svcInfo.onlyNodeLocalEndpoints {
			continue
		}

		// Now write ingress loadbalancing & DNAT rules only for services that request OnlyLocal traffic.
		// TODO - This logic may be combinable with the block above that creates the svc balancer chain
		localEndpoints := make([]*endpointsInfo, 0)
		localEndpointChains := make([]utiliptables.Chain, 0)
		for i := range endpointChains {
			if endpoints[i].isLocal {
				// These slices parallel each other; must be kept in sync
				localEndpoints = append(localEndpoints, endpoints[i])
				localEndpointChains = append(localEndpointChains, endpointChains[i])
			}
		}
		// First rule in the chain redirects all pod -> external VIP traffic to the
		// Service's ClusterIP instead. This happens whether or not we have local
		// endpoints; only if clusterCIDR is specified
		if len(proxier.clusterCIDR) > 0 {
			args = append(args[:0],
				"-A", string(svcXlbChain),
				"-m", "comment", "--comment",
				`"Redirect pods trying to reach external loadbalancer VIP to clusterIP"`,
				"-s", proxier.clusterCIDR,
				"-j", string(svcChain),
			)
			writeLine(proxier.natRules, args...)
		}

		numLocalEndpoints := len(localEndpointChains)
		if numLocalEndpoints == 0 {
			// Blackhole all traffic since there are no local endpoints
			args = append(args[:0],
				"-A", string(svcXlbChain),
				"-m", "comment", "--comment",
				fmt.Sprintf(`"%s has no local endpoints"`, svcNameString),
				"-j",
				string(KubeMarkDropChain),
			)
			writeLine(proxier.natRules, args...)
		} else {
			// First write session affinity rules only over local endpoints, if applicable.
			if svcInfo.sessionAffinityType == api.ServiceAffinityClientIP {
				for _, endpointChain := range localEndpointChains {
					writeLine(proxier.natRules,
						"-A", string(svcXlbChain),
						"-m", "comment", "--comment", svcNameString,
						"-m", "recent", "--name", string(endpointChain),
						"--rcheck", "--seconds", strconv.Itoa(svcInfo.stickyMaxAgeSeconds), "--reap",
						"-j", string(endpointChain))
				}
			}

			// Setup probability filter rules only over local endpoints
			for i, endpointChain := range localEndpointChains {
				// Balancing rules in the per-service chain.
				args = append(args[:0],
					"-A", string(svcXlbChain),
					"-m", "comment", "--comment",
					fmt.Sprintf(`"Balancing rule %d for %s"`, i, svcNameString),
				)
				if i < (numLocalEndpoints - 1) {
					// Each rule is a probabilistic match.
					args = append(args,
						"-m", "statistic",
						"--mode", "random",
						"--probability", proxier.probability(numLocalEndpoints-i))
				}
				// The final (or only if n == 1) rule is a guaranteed match.
				args = append(args, "-j", string(endpointChain))
				writeLine(proxier.natRules, args...)
			}
		}
	}

	// Delete chains no longer in use.
	for chain := range existingNATChains {
		if !activeNATChains[chain] {
			chainString := string(chain)
			if !strings.HasPrefix(chainString, "KUBE-SVC-") && !strings.HasPrefix(chainString, "KUBE-SEP-") && !strings.HasPrefix(chainString, "KUBE-FW-") && !strings.HasPrefix(chainString, "KUBE-XLB-") {
				// Ignore chains that aren't ours.
				continue
			}
			// We must (as per iptables) write a chain-line for it, which has
			// the nice effect of flushing the chain.  Then we can remove the
			// chain.
			writeLine(proxier.natChains, existingNATChains[chain])
			writeLine(proxier.natRules, "-X", chainString)
		}
	}

	// Finally, tail-call to the nodeports chain.  This needs to be after all
	// other service portal rules.
	writeLine(proxier.natRules,
		"-A", string(kubeServicesChain),
		"-m", "comment", "--comment", `"kubernetes service nodeports; NOTE: this must be the last rule in this chain"`,
		"-m", "addrtype", "--dst-type", "LOCAL",
		"-j", string(kubeNodePortsChain))

	// If the masqueradeMark has been added then we want to forward that same
	// traffic, this allows NodePort traffic to be forwarded even if the default
	// FORWARD policy is not accept.
	writeLine(proxier.filterRules,
		"-A", string(kubeForwardChain),
		"-m", "comment", "--comment", `"kubernetes forwarding rules"`,
		"-m", "mark", "--mark", proxier.masqueradeMark,
		"-j", "ACCEPT",
	)

	// The following rules can only be set if clusterCIDR has been defined.
	if len(proxier.clusterCIDR) != 0 {
		// The following two rules ensure the traffic after the initial packet
		// accepted by the "kubernetes forwarding rules" rule above will be
		// accepted, to be as specific as possible the traffic must be sourced
		// or destined to the clusterCIDR (to/from a pod).
		writeLine(proxier.filterRules,
			"-A", string(kubeForwardChain),
			"-s", proxier.clusterCIDR,
			"-m", "comment", "--comment", `"kubernetes forwarding conntrack pod source rule"`,
			"-m", "conntrack",
			"--ctstate", "RELATED,ESTABLISHED",
			"-j", "ACCEPT",
		)
		writeLine(proxier.filterRules,
			"-A", string(kubeForwardChain),
			"-m", "comment", "--comment", `"kubernetes forwarding conntrack pod destination rule"`,
			"-d", proxier.clusterCIDR,
			"-m", "conntrack",
			"--ctstate", "RELATED,ESTABLISHED",
			"-j", "ACCEPT",
		)
	}

	// Write the end-of-table markers.
	writeLine(proxier.filterRules, "COMMIT")
	writeLine(proxier.natRules, "COMMIT")

	// Sync rules.
	// NOTE: NoFlushTables is used so we don't flush non-kubernetes chains in the table
	proxier.iptablesData.Reset()
	proxier.iptablesData.Write(proxier.filterChains.Bytes())
	proxier.iptablesData.Write(proxier.filterRules.Bytes())
	proxier.iptablesData.Write(proxier.natChains.Bytes())
	proxier.iptablesData.Write(proxier.natRules.Bytes())

	glog.V(5).Infof("Restoring iptables rules: %s", proxier.iptablesData.Bytes())
	err = proxier.iptables.RestoreAll(proxier.iptablesData.Bytes(), utiliptables.NoFlushTables, utiliptables.RestoreCounters)
	if err != nil {
		glog.Errorf("Failed to execute iptables-restore: %v", err)
		// Revert new local ports.
		glog.V(2).Infof("Closing local ports after iptables-restore failure")
		utilproxy.RevertPorts(replacementPortsMap, proxier.portsMap)
		return
	}

	// Close old local ports and save new ones.
	for k, v := range proxier.portsMap {
		if replacementPortsMap[k] == nil {
			v.Close()
		}
	}
	proxier.portsMap = replacementPortsMap

	// Update healthz timestamp.
	if proxier.healthzServer != nil {
		proxier.healthzServer.UpdateTimestamp()
	}

	// Update healthchecks.  The endpoints list might include services that are
	// not "OnlyLocal", but the services list will not, and the healthChecker
	// will just drop those endpoints.
	if err := proxier.healthChecker.SyncServices(serviceUpdateResult.HCServiceNodePorts); err != nil {
		glog.Errorf("Error syncing healtcheck services: %v", err)
	}
	if err := proxier.healthChecker.SyncEndpoints(endpointUpdateResult.HCEndpointsLocalIPSize); err != nil {
		glog.Errorf("Error syncing healthcheck endoints: %v", err)
	}

	// Finish housekeeping.
	// TODO: these could be made more consistent.
	for _, svcIP := range staleServices.UnsortedList() {
		if err := utilproxy.ClearUDPConntrackForIP(proxier.exec, svcIP); err != nil {
			glog.Errorf("Failed to delete stale service IP %s connections, error: %v", svcIP, err)
		}
	}
	proxier.deleteEndpointConnections(endpointUpdateResult.StaleEndpoints)
}

// Join all words with spaces, terminate with newline and write to buf.
func writeLine(buf *bytes.Buffer, words ...string) {
	// We avoid strings.Join for performance reasons.
	for i := range words {
		buf.WriteString(words[i])
		if i < len(words)-1 {
			buf.WriteByte(' ')
		} else {
			buf.WriteByte('\n')
		}
	}
}

func openLocalPort(lp *utilproxy.LocalPort) (utilproxy.Closeable, error) {
	// For ports on node IPs, open the actual port and hold it, even though we
	// use iptables to redirect traffic.
	// This ensures a) that it's safe to use that port and b) that (a) stays
	// true.  The risk is that some process on the node (e.g. sshd or kubelet)
	// is using a port and we give that same port out to a Service.  That would
	// be bad because iptables would silently claim the traffic but the process
	// would never know.
	// NOTE: We should not need to have a real listen()ing socket - bind()
	// should be enough, but I can't figure out a way to e2e test without
	// it.  Tools like 'ss' and 'netstat' do not show sockets that are
	// bind()ed but not listen()ed, and at least the default debian netcat
	// has no way to avoid about 10 seconds of retries.
	var socket utilproxy.Closeable
	switch lp.Protocol {
	case "tcp":
		listener, err := net.Listen("tcp", net.JoinHostPort(lp.IP, strconv.Itoa(lp.Port)))
		if err != nil {
			return nil, err
		}
		socket = listener
	case "udp":
		addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(lp.IP, strconv.Itoa(lp.Port)))
		if err != nil {
			return nil, err
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			return nil, err
		}
		socket = conn
	default:
		return nil, fmt.Errorf("unknown protocol %q", lp.Protocol)
	}
	glog.V(2).Infof("Opened local port %s", lp.String())
	return socket, nil
}
