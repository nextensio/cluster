package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	common "gitlab.com/nextensio/common/go"
	"gitlab.com/nextensio/common/go/messages/nxthdr"
	nhttp2 "gitlab.com/nextensio/common/go/transport/http2"
	websock "gitlab.com/nextensio/common/go/transport/websocket"
	"go.uber.org/zap"
	"minion.io/consul"
	"minion.io/policy"
	"minion.io/shared"

	"github.com/google/uuid"

	opentracing "github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	jaegercfg "github.com/uber/jaeger-client-go/config"
	jaegerlog "github.com/uber/jaeger-client-go/log"
	jaegermetrics "github.com/uber/jaeger-lib/metrics/prometheus"
)

// NOTE1: About all the multitude of UUIDs.
// onboard.userid: This everyone understands - its like foobar@nextensio.net
//
// onboard.Uuid: foobar@nextensio.net can have three devices, one laptop, one
// ipad and one android phone. So when foobar connects to nextensio from all these devices,
// each device will get a unique id, wihch is onboard.Uuid
//
// Suuid: Now foobar connecting from laptop might have multiple tunnels to the cluster - either
// because we support multiple tunnels from the same device (eventually, not yet), or it can be
// transient situations where one tunnel is going down and not cleaned up fully while the other
// tunnel is coming up etc.. - this can very well happen because each tunnel is handled by
// independent goroutines. Suuid means 'session UUid' - an identifier to the tunnel itself. There
// can be many flows over the same tunnel.

// NOTE2: About metrics
// Each user has a totalBytes metric (for now). And the metric has a bunch of labels which will enable
// to slice and dice the user's traffic based on which service the user is accessing, the direction
// etc.. Prometheus very CLEARLY says not have label cardinality at the level of a userid or else the
// time series will bloat and become unmanageable, hence the reason we have a metric per userid and
// then a few labels underneath

// NOTE3: About the nxthdr.NxtFlow structure and its fields
// We always keep the flow structure fields (most of it) the same regardless of whether the flow
// is upstream or downstream - we keep the flow field value the same as it is
// when  going from agent to connector.
//
// The above intent is achieved by ensuring that the data in the "nxthdr.NxtFlow" is preserved
// when going upstream and coming back. That is, the flow source/dest/port etc.. remains the same
// value going up and coming down. The two things that gets modified are
// 1. the SourceAgent/DestAgent: If we look at the connector code, the connector swaps the
//    SourceAgent/DestAgent before sending a response back to the cpod.
// 2. The flow.UserCluster and flow.UserPod - the cpod sets it to its own pod information before
//    sending the response back to apod

type zap2log struct {
	s *zap.SugaredLogger
}

func (z *zap2log) Write(p []byte) (n int, err error) {
	z.s.Debugf(string(p))
	return len(p), nil
}

type podInfo struct {
	pending bool
	tunnel  common.Transport
}

type agentTunnel struct {
	tunnel common.Transport
	suuid  uuid.UUID
}

type userMetrics struct {
	count      uint
	userid     string
	totalBytes *prometheus.CounterVec
	attr       *prometheus.CounterVec
}

type deleteMetrics struct {
	um    *userMetrics
	flow  *nxthdr.NxtFlow
	qedAt time.Time
}

type flowMetrics struct {
	bytes *prometheus.Counter
	attr  *prometheus.Counter
}

// Default prometheus scrape interval is one minute, we give additional 30 seconds
const PROM_SCRAPE_TIMER = 90

// TODO: The aLock is a big fat lock here and is liberally taken during the entire duration
// of an agent add/del operation - including the time spent registering/deregistering with
// consul etc.. The add and del of an agent can happen in parallel - one agent tunnel might
// be going down and at the same time a new tunnel from the same agent might be coming up.
// So it is VERY IMPORTANT to take the locks for a single agent and to serialize them properly,
// but we do not have to take this big fat lock for that reason - so we really need a per-agent
// lock also so that one agent going up down does not block another agent. So the big lock
// should be taken just when the global arrays are mutated/changed, and the per-agent lock
// should be taken during the time the agent consul/route/whatever other operations are done
var aLock sync.RWMutex
var sessions map[uuid.UUID]nxthdr.NxtOnboard
var agents map[string]*agentTunnel
var users map[string][]*agentTunnel
var mLock sync.RWMutex
var metrics map[string]*userMetrics
var pLock sync.RWMutex
var pods map[string]*podInfo
var unusedChan chan common.NxtStream
var outsideMsg chan bool
var insideMsg chan bool
var routeLock sync.RWMutex
var localRoutes map[string]*nxthdr.NxtOnboard
var totGoroutines int32
var insideOpen bool
var pendingLock sync.Mutex
var pendingFree []*deleteMetrics
var wireTracer opentracing.Tracer

// When the flow is terminated, we have to wait for some time to ensure the stats is
// collected before we remove the labels/free the counters, we just use a simple
// pending list for that which is garbage collected every couple of minutes
func pendingAdd(um *userMetrics, flow *nxthdr.NxtFlow) {
	pendingLock.Lock()
	defer pendingLock.Unlock()
	d := deleteMetrics{
		um:    um,
		flow:  flow,
		qedAt: time.Now(),
	}
	pendingFree = append(pendingFree, &d)
}

func pendingExpired() *deleteMetrics {
	pendingLock.Lock()
	defer pendingLock.Unlock()
	if len(pendingFree) == 0 {
		return nil
	}
	// The head is the oldest flow, if head has not expired, nothing
	// else has expired
	elapsed := time.Since(pendingFree[0].qedAt)
	if elapsed < time.Duration(PROM_SCRAPE_TIMER*time.Second) {
		return nil
	}
	dm := pendingFree[0]
	pendingFree = pendingFree[1:]
	return dm
}

func garbageCollectFlows(s *zap.SugaredLogger) {
	for {
		for {
			dm := pendingExpired()
			if dm == nil {
				break
			}
			metricFlowDel(s, dm.um, dm.flow)
		}
		time.Sleep(5 * time.Second)
	}
}

// Build the User Attribute Metrics labels name:value map used to instantiate,
// identify and manage the prometheus metrics. These labels provide dimensions
// for the metrics that are based on nextensio user attributes defined at the
// controller and thus obtained at runtime.  Note that presently this is done
// once - when the user stream is first created. Still TBD is supporting
// updates to these attributes during the lifetime of the stream.
func userAttrMetricsLabelInit(s *zap.SugaredLogger, flow *nxthdr.NxtFlow) map[string]string {
	var uaLabels map[string]interface{}
	var uaLabelMap = make(map[string]string)

	if flow.Usrattr == "" {
		s.Debugf("UAM - No flow.Usrattr")
		return nil
	}

	uab := []byte(flow.Usrattr)
	err := json.Unmarshal(uab, &uaLabels)
	if err != nil {
		s.Debugf("Error decoding attributes", err)
		return nil
	}

	for lname, lvalue := range uaLabels {
		switch tlvalue := lvalue.(type) {
		case string:
			uaLabelMap[lname] = tlvalue
		case bool:
			uaLabelMap[lname] = strconv.FormatBool(tlvalue)
		case float64:
			uaLabelMap[lname] = strconv.FormatFloat(tlvalue, 'f', 2, 64)
		case float32:
			uaLabelMap[lname] = strconv.FormatFloat(float64(tlvalue), 'f', 2, 32)
		case int64:
			uaLabelMap[lname] = strconv.FormatInt(tlvalue, 10)
		case int32:
			uaLabelMap[lname] = strconv.FormatInt(int64(tlvalue), 10)
		case []interface{}:
			// We have an array of values of type "one of the above". Presently
			// it isn't clear what the best way to handle these is. A
			// couple options: 1) make a single string value
			// by appending the N array elements together 2) take just one.
			// For now implementing the former.
			var sbLabelValue bytes.Buffer
			for _, v := range tlvalue {
				switch tv := v.(type) {
				case string:
					sbLabelValue.WriteString(tv + ",")
				case bool:
					sbLabelValue.WriteString(strconv.FormatBool(tv) + ",")
				case float64:
					sbLabelValue.WriteString(strconv.FormatFloat(tv, 'f', 2, 64) + ",")
				case float32:
					sbLabelValue.WriteString(strconv.FormatFloat(float64(tv), 'f', 2, 32) + ",")
				case int64:
					sbLabelValue.WriteString(strconv.FormatInt(tv, 10) + ",")
				case int32:
					sbLabelValue.WriteString(strconv.FormatInt(int64(tv), 10) + ",")
				default:
					s.Debugf("UAM-Only one level of User Attribute nesting supported")
				}
			}
			uaLabelMap[lname] = sbLabelValue.String()
		default:
			s.Debugf("UAM-%v is an unexpected type that needs handling", tlvalue)
		}
	}
	return uaLabelMap
}

// The metrics per-user are created when we see a new flow come in from that user
// (either on apod or cpod, logic is same on both) and we keep a reference count of
// how many flows are using that userMetric structure (there is a count field there).
// So for every flow that ends up calling a metricUserGet(), there should be an equivalent
// metricUserPut() when the flow is done or there is some failure, or else there will
// be a memory leak of that structure.
func metricUserNew(s *zap.SugaredLogger, userid string, uamLabels map[string]string) *userMetrics {
	var labels = []string{"userid", "service", "destIP", "destPort", "direction"}

	// Prometheus wont allow certain characters in the string as the counter
	name := strings.Replace(userid, "@", "_", -1)
	name = strings.Replace(name, ".", "_", -1)
	name = strings.Replace(name, "-", "_", -1)
	totalBytes := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bytes_" + name,
			Help: "User Data Bytes",
		}, labels)

	err := prometheus.Register(totalBytes)
	if err != nil {
		s.Debugf("Prometheus registration failed", err, userid, name)
		return nil
	}

	// TODO: attributes for a user can change on the fly, so the registration
	// here will need to be re-done when that happens
	// (https://gitlab.com/nextensio/cluster/-/issues/41)
	var uamLabelNames []string
	// We only need the names for registration
	for l := range uamLabels {
		uamLabelNames = append(uamLabelNames, l)
	}

	var attr *prometheus.CounterVec
	if uamLabelNames != nil {
		// Now register the metric w/prometheus
		attr = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name:        "attributes_" + name,
				Help:        "User Attributes",
				ConstLabels: prometheus.Labels{},
			}, uamLabelNames)
		err = prometheus.Register(attr)
		if err != nil {
			s.Debugf("Prometheus UAMetric registration failed(%v) ", err)
			prometheus.Unregister(totalBytes)
			return nil
		}
	}

	m := &userMetrics{count: 1, userid: userid, totalBytes: totalBytes, attr: attr}

	metrics[userid] = m
	return m
}

func metricUserPut(m *userMetrics) {
	mLock.Lock()
	defer mLock.Unlock()

	m.count -= 1
	if m.count != 0 {
		return
	}
	m.totalBytes.Reset()
	prometheus.Unregister(m.totalBytes)
	if m.attr != nil {
		m.attr.Reset()
		prometheus.Unregister(m.attr)
	}

	delete(metrics, m.userid)
}

func metricUserGet(s *zap.SugaredLogger, userid string, uamLabels map[string]string) *userMetrics {
	mLock.Lock()
	defer mLock.Unlock()

	m := metrics[userid]
	if m == nil {
		return metricUserNew(s, userid, uamLabels)
	}
	m.count++

	return m
}

func getLabels(flow *nxthdr.NxtFlow) map[string]string {
	labels := make(map[string]string)
	labels["userid"] = flow.AgentUuid
	labels["service"] = flow.DestSvc
	labels["destIP"] = flow.Dest
	labels["destPort"] = strconv.FormatUint(uint64(flow.Dport), 10)
	if !flow.ResponseData {
		labels["direction"] = "TO_CLOUD"
	} else {
		labels["direction"] = "FROM_CLOUD"
	}
	return labels
}

func incrMetrics(fm *flowMetrics, length int) {

	if fm.bytes != nil {
		(*fm.bytes).Add(float64(length))
	}
	if fm.attr != nil {
		(*fm.attr).Inc()
	}
}

func metricFlowDel(s *zap.SugaredLogger, m *userMetrics, flow *nxthdr.NxtFlow) {
	labels := getLabels(flow)
	m.totalBytes.Delete(labels)
	if m.attr != nil {
		uamLabels := userAttrMetricsLabelInit(s, flow)
		m.attr.Delete(uamLabels)
	}
	metricUserPut(m)
}

func metricFlowAdd(s *zap.SugaredLogger, flow *nxthdr.NxtFlow) (*userMetrics, *flowMetrics) {
	// agentUuid is userid:uniqueid
	user := strings.Split(flow.AgentUuid, ":")
	if len(user) != 2 {
		s.Debugf("Bad userid", flow.AgentUuid)
		return nil, nil
	}
	// In forward direction, the attributes are that of the user, in
	// response/reverse direction, attributes are of the connector,
	// we "can" count attributes against connector, but really does it
	// matter to customer the ability to see connector's attributes ?
	// Also attribute metrics dont have much of a value being directional,
	// customer wants to see what attributes are in use, the directional
	// count is anyways provided by byte counter.
	var uamLabels map[string]string = nil
	if !flow.ResponseData {
		uamLabels = userAttrMetricsLabelInit(s, flow)
	}
	m := metricUserGet(s, user[0], uamLabels)
	if m == nil {
		s.Debugf("Cant find prometheus counter vec", flow.AgentUuid)
		return nil, nil
	}
	labels := getLabels(flow)
	c, e := m.totalBytes.GetMetricWith(labels)
	if e != nil {
		metricUserPut(m)
		s.Debugf("Cant get prometheus counter from labels", labels, flow.AgentUuid)
		return nil, nil
	}
	var attr *prometheus.Counter
	if m.attr != nil && !flow.ResponseData {
		a, err := m.attr.GetMetricWith(uamLabels)
		if err != nil {
			s.Debugf("Error getting attr metric", err)
		} else {
			attr = &a
		}
	}
	return m, &flowMetrics{bytes: &c, attr: attr}
}

func sessionAdd(Suuid uuid.UUID, onboard *nxthdr.NxtOnboard) {
	aLock.Lock()
	defer aLock.Unlock()

	sessions[Suuid] = *onboard
}

func sessionDel(Suuid uuid.UUID) {
	aLock.Lock()
	defer aLock.Unlock()

	delete(sessions, Suuid)
}

func sessionGet(Suuid uuid.UUID) *nxthdr.NxtOnboard {
	var onboard *nxthdr.NxtOnboard
	aLock.RLock()
	defer aLock.RUnlock()

	if o, ok := sessions[Suuid]; ok {
		onboard = &o
	}
	return onboard
}

func atype(onboard *nxthdr.NxtOnboard) string {
	if onboard.Agent {
		return "agent"
	} else {
		return "connector"
	}
}

func agentAdd(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, Suuid uuid.UUID, tunnel common.Transport) error {
	aLock.Lock()
	defer aLock.Unlock()

	// The reason we allow only one connector per cpod is because connectors are
	// few in number (say in hundreds), and has stable long lived connections.
	// So we dont want a scenario where we allocated 100 replicas, and there are 100
	// connectors, but all of them got loadbalanced to just ten of the replicas! And
	// moreover, if we have more than one connector in this pod, now we have to figure
	// out how to loadbalance among them.
	if MyInfo.PodType == "cpod" {
		if len(agents) != 0 {
			s.Debugf("More than one agent", agents)
			return fmt.Errorf("Only one connector allowed per cpod")
		}
	}

	info := policy.UserInfo{
		Userid:  onboard.Userid,
		Cluster: onboard.Cluster,
		Podname: onboard.Podname,
	}
	if !policy.NxtUsrAllowed(atype(onboard), info) {
		return fmt.Errorf("User disallowed")
	}

	// Here is where we assume there is only one active tunnel from an agent device to the cluster,
	// so if that needs to change in future, this is the place to come looking for
	agentT := &agentTunnel{tunnel: tunnel, suuid: Suuid}
	agents[onboard.Uuid] = agentT
	cur := users[onboard.Userid]
	users[onboard.Userid] = append(cur, agentT)
	localRouteAdd(s, MyInfo, onboard)
	// Do not register any service with Consul for users, only for connectors.
	if !onboard.Agent {
		err := consul.RegisterConsul(MyInfo, onboard.Services, s)
		if err != nil {
			consul.DeRegisterConsul(MyInfo, onboard.Services, s)
			return err
		}
	}

	// First connector in this cpod, disallow any more connectors, and allow
	// apods to connect to this cpod
	if MyInfo.PodType == "cpod" {
		if len(agents) == 1 {
			outsideMsg <- false
			insideMsg <- true
		}
	}

	s.Debugf("AgentAdd: user %s, Suuid %s, total user tunnels %d, total agents %d", onboard.Userid, Suuid,
		len(users[onboard.Userid]), len(agents))
	return nil
}

func agentDel(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, Suuid uuid.UUID) error {
	aLock.Lock()
	defer aLock.Unlock()

	var err error
	agentT := agents[onboard.Uuid]
	// Here is where we assume there is only one active tunnel from an agent device to the cluster,
	// so if that needs to change in future, this is the place to come looking for.
	// NOTE: We check for the Suuid here because it can happen that from the same device
	// 1. A tunnel A was established (suuid X)
	// 2. The tunnel A was torn down (suuid X)
	// 3. New tunnel B is establshed (suuid Y)
	// But on the minion side since these are all asynchronous go routines, it can end up being
	// processed reordered as below
	// 1. A tunnel A was established (suuid X)
	// 2. A Tunnel B is established (suuid Y) - so this overwrites the agents[onboard.UUID] with tunnel B
	// 3. Now tunnel A is torn down, now we shouldnt end up removing tunnel B !
	if agentT != nil && agentT.suuid == Suuid {
		delete(agents, onboard.Uuid)
		localRouteDel(s, MyInfo, onboard)
	}
	// No more connectors in this pod, allow new ones to come in from outside,
	// and disallow apods to try and connect here from inside
	if MyInfo.PodType == "cpod" {
		if len(agents) == 0 {
			outsideMsg <- true
			insideMsg <- false
		}
	}
	cur := users[onboard.Userid]
	lcur := len(cur)
	for i := 0; i < lcur; i++ {
		if cur[i].suuid == Suuid {
			users[onboard.Userid] = append(cur[:i], cur[i+1:]...)
			break
		}
	}
	if len(users[onboard.Userid]) == 0 {
		// No more connections from this userid
		delete(users, onboard.Userid)
		if !onboard.Agent {
			// Deregister services from Consul if it's a connector
			err = consul.DeRegisterConsul(MyInfo, onboard.Services, s)
		}
		policy.NxtUsrLeave(atype(onboard), onboard.Userid)
	}
	s.Debugf("AgentDel: err %s, user %s, Suuid %s, total user tunnels prev %d / new %d, total agents  %d",
		err, onboard.Userid, Suuid, lcur, len(users[onboard.Userid]), len(agents))
	return err
}

func agentTunnelGet(uuid string) common.Transport {
	aLock.RLock()
	defer aLock.RUnlock()

	agentT := agents[uuid]
	if agentT == nil {
		return nil
	}
	return agentT.tunnel
}

// Lookup a session to the agent/connector on this same pod.
func localRouteLookup(s *zap.SugaredLogger, MyInfo *shared.Params, flow *nxthdr.NxtFlow) (*nxthdr.NxtOnboard, common.Transport) {
	// TODO: This dot dash business has to go away, we have to unify the usage
	// of services everywhere to either dot or dash
	service := strings.ReplaceAll(flow.DestAgent, ".", "-")
	// There can be multiple agents (devices) with the same userid, we have to get the flow
	// back to the exact agent that originated it - that is on the apod. But on the cpod we
	// will have just one connector per pod
	if flow.ResponseData && MyInfo.PodType == "apod" {
		service = service + flow.AgentUuid
	}
	routeLock.RLock()
	onboard := localRoutes[service]
	routeLock.RUnlock()

	if onboard == nil {
		return nil, nil
	}
	tunnel := agentTunnelGet(onboard.Uuid)
	if tunnel == nil {
		return nil, nil
	}
	return onboard, tunnel
}

// Lookup a session to the local/remote pod/cluster + destAgent combo, if none exists then
// create a new session to the pod on the local/remote cluster that hosts the agent/connector
// identified by destAgent. The session creation is slow because of tcp/tls negotiation etc..
// The next flow will find an existing session and just create a new stream over it, which is
// a very fast operation compared to the tcp/tls negotiation for creating a session.
//
// NOTE NOTE NOTE: The way istio/envoy handles http2 sessions/streams can be confusing. Lets
// say we have two remote pods cpod1 and cpod2. Now from apod1 we first want to send traffic
// to cpod1. So we open an http2 session to remote-gateway, add x-nextensio-for: cpod1 and that
// stream reaches cpod1. Now if we want to send to cpod2, one might logically assume that we
// can use the same session as above because that just takes the packet to the remote-gateway
// ingress istio gateway and then if we add a different x-nextensio-for: cpod2, it will reach
// cpod2. But that assumption is erroneous, as can be easily verified with experimentation.
// From remote gateway istio/envoy ingress perpsective, once a "session" is established to a
// pod (cpod1 in our example), we can open thousands of streams on that session with all kinds of
// different http headers, but all those streams will only go to cpod1. And hence the reason
// why wwe key here using the destination + pod combo.
// See https://istio.io/latest/blog/2021/zero-config-istio/ for more details. There they say that
// a session to "one backend" can then loadbalance streams to multiple replicas in that backend.
// Theoretically a session to an ingress gateway should be able to load balance to multiple
// backends too, but its not clear from that article whether they do that. Till we know that for
// sure, we will open a session to each "backend" (ie pod)
func podLookup(s *zap.SugaredLogger, ctx context.Context, MyInfo *shared.Params,
	onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow, fwd shared.Fwd) common.Transport {
	var tunnel common.Transport
	var pending bool = false

	key := fwd.Id + ":" + fwd.Pod

	// Try read lock first since the most common case should be that tunnels are
	// already setup
	pLock.RLock()
	p := pods[key]
	if p != nil {
		tunnel = p.tunnel
		pending = p.pending
	}
	pLock.RUnlock()

	// The uncommon/infrequent case. Multiple goroutines will try to connect to the
	// same pod, so we should not end up creating multiple tunnels to the same pod,
	// hence the pending flag to ensure only one tunnel is created
	if tunnel == nil && !pending {
		pLock.Lock()
		p = pods[key]
		if p == nil {
			pods[key] = &podInfo{pending: true, tunnel: nil}
			podDial(s, ctx, MyInfo, onboard, flow, fwd, key)
			tunnel = pods[key].tunnel
		} else {
			tunnel = p.tunnel
		}
		pLock.Unlock()
	}

	return tunnel
}

// The concept is that there is one "session" to the destination over which there are
// "streams", and if the "session" is closed for some reason then we have to create a
// new one. For http2 in specific, we dont really need this tunnel monitor, because
// in golang http2 lib, everything is a "stream" - what we call here as the main "session"
// is just a stream. And a NewStream() over it just ends up doing an http2 request which
// the lib will smartly figure out whether the existing session is closed, a new session
// is needed etc.. But if not for http2, if we are using some other transport, we need to
// keep this semantics intact of session + stream and monitoring a session etc..
func podTunnelMonitor(s *zap.SugaredLogger, key string, tunnel common.Transport, fwd shared.Fwd) {
	hdr := &nxthdr.NxtHdr{}
	hdr.Hdr = &nxthdr.NxtHdr_Keepalive{}
	agentBuf := net.Buffers{}

	for {
		err := tunnel.Write(hdr, agentBuf)
		if err != nil {
			tunnel.Close()
			s.Debugf("Monitor tunnel closed", err, key)
			pLock.Lock()
			pods[key] = nil
			pLock.Unlock()
			return
		}
		time.Sleep(10 * time.Second)
	}
}

// Create the initial session to a destination pod/remote cluser
func podDial(s *zap.SugaredLogger, ctx context.Context, MyInfo *shared.Params,
	onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow, fwd shared.Fwd, key string) {
	var pubKey []byte

	// Add headers for tunnel between source pod and destination pod or cluster
	hdrs := make(http.Header)
	hdrs.Add("x-nextensio-sourcecluster", MyInfo.Id)
	hdrs.Add("x-nextensio-sourcepod", MyInfo.Host)
	hdrs.Add("x-nextensio-destcluster", fwd.Id)
	// This is the header that gets this flow to the right pod
	hdrs.Add("x-nextensio-for", fwd.Pod)

	lg := log.New(&zap2log{s: s}, "http2", 0)
	client := nhttp2.NewClient(ctx, lg, pubKey, fwd.Dest, fwd.Dest, MyInfo.Iport, hdrs, &totGoroutines)
	// For pod to pod connectivity, a pod will always dial-out another one,
	// we dont expect a stream to come in from the other end on a dial-out session,
	// and hence the reason we use the unusedChan on which no one is listening.
	// This is a unidirectional session, we just write on this. Writing from the
	// other end will end up with the other end dialling a new connection
	err := client.Dial(unusedChan)
	if err != nil {
		s.Debugf("Pod dialing error for %s at %s", err, flow.DestAgent, fwd.Dest)
		return
	}
	s.Debugf("Dialed pod for %s at %s", flow.DestAgent, fwd.Dest)

	pods[key].pending = false
	pods[key].tunnel = client
	go podTunnelMonitor(s, key, client, fwd)
}

func localOneRouteAdd(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, svc string) {
	svc = strings.ReplaceAll(svc, ".", "-")
	// Multiple agents can login with the same userid, each agent tunnel has a unique uuid
	if onboard.Agent {
		svc = svc + onboard.Userid + ":" + onboard.Uuid
	}
	routeLock.Lock()
	localRoutes[svc] = onboard
	routeLock.Unlock()
}

func localRouteAdd(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	// Register to the local routing table so that we know what services are
	// available on this pod.
	for _, s := range onboard.Services {
		localOneRouteAdd(sl, MyInfo, onboard, s)
	}
	// The agents and connectors sets flow.SourceAgent field to their connectId. The return
	// traffic will look for a tunnel with that name in the route table
	localOneRouteAdd(sl, MyInfo, onboard, onboard.ConnectId)
}

func localOneRouteDel(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, svc string) {
	svc = strings.ReplaceAll(svc, ".", "-")
	// Multiple agents can login with the same userid, each agent tunnel has a unique uuid
	if onboard.Agent {
		svc = svc + onboard.Uuid
	}
	routeLock.Lock()
	localRoutes[svc] = nil
	routeLock.Unlock()
}

func localRouteDel(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	for _, s := range onboard.Services {
		localOneRouteDel(sl, MyInfo, onboard, s)
	}
	localOneRouteDel(sl, MyInfo, onboard, onboard.ConnectId)
}

// Lookup destination stream to send an L4 tcp/udp/http proxy packet on
func globalRouteLookup(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context,
	onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow, span *opentracing.Span) (*nxthdr.NxtOnboard,
	common.Transport) {
	var consul_key, tag string
	var fwd shared.Fwd
	var err error

	// Do a Consul DNS lookup for the user -> connector direction only
	// For the reply connector -> user direction, the target cluster and pod are
	// obtained from the flow header fields UserCluster and UserPod. UserPod is set
	// as the value of the x-nextensio-for header.
	if !flow.ResponseData {
		// We're on an Apod, trying to send the frame to a Cpod
		tag = policy.NxtRouteLookup(atype(onboard), onboard.Userid, flow.Usrattr, flow.DestAgent)
		if tag == "deny" {
			s.Debugf("Agent %s access to service %s denied",
				onboard.Userid, flow.DestAgent)
			return nil, nil
		}

		host := strings.ReplaceAll(flow.DestAgent, ".", "-")
		if tag == "" {
			consul_key = strings.Join([]string{host, MyInfo.Namespace}, "-")
		} else {
			consul_key = strings.Join([]string{tag, host, MyInfo.Namespace}, "-")
			flow.DestAgent = tag + "." + flow.DestSvc
		}

		fwd, err = consul.ConsulDnsLookup(MyInfo, consul_key, s)
		if err != nil {
			s.Debugf("Consul lookup failed for dest %s with key %s", flow.DestAgent, consul_key)
			return nil, nil
		}
	} else {
		if flow.UserCluster == MyInfo.Id {
			// Local destination
			fwd.DestType = shared.LocalDest
			fwd.Dest = flow.UserPod + "-in." + MyInfo.Namespace + consul.LocalSuffix
		} else {
			// Remote destination
			fwd.DestType = shared.RemoteDest
			fwd.Dest = flow.UserCluster + consul.RemotePostPrefix
		}
		fwd.Pod = flow.UserPod
		fwd.Id = flow.UserCluster
	}

	// Add http headers specific to this session/stream
	hdrs := make(http.Header)
	// Add following headers for stats dimensions:
	hdrs.Add("x-nextensio-sourceagent", flow.SourceAgent)
	hdrs.Add("x-nextensio-userid", onboard.Userid)
	if span != nil {
		setSpanTags(*span, flow, MyInfo)
		(*span).Tracer().Inject(
			(*span).Context(),
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(hdrs),
		)
		if !flow.ResponseData {
			// On apod, set flow.TraceCtx to Uber-trace-id header injected
			flow.TraceCtx = hdrs.Get("Uber-Trace-Id")
		}
	}

	switch fwd.DestType {
	case shared.SelfDest:
		bundle, dest := localRouteLookup(s, MyInfo, flow)
		if dest != nil {
			return bundle, dest.NewStream(nil)
		}
	case shared.LocalDest:
		dest := podLookup(s, ctx, MyInfo, onboard, flow, fwd)
		if dest != nil {
			return nil, dest.NewStream(hdrs)
		}
	case shared.RemoteDest:
		dest := podLookup(s, ctx, MyInfo, onboard, flow, fwd)
		if dest != nil {
			return nil, dest.NewStream(hdrs)
		}
	}

	return nil, nil
}

func setSpanTags(span opentracing.Span, flow *nxthdr.NxtFlow, MyInfo *shared.Params) {
	spanuattrs := make(map[string]interface{})
	tReq := strings.SplitN(flow.TraceRequestId, ":", 2)
	span.SetTag("nxt-trace-requestid", tReq[0])
	span.SetTag("nxt-trace-source", MyInfo.Id+"-"+MyInfo.Host)
	span.SetTag("nxt-trace-destagent", flow.DestAgent)
	span.SetTag("nxt-trace-userid", flow.Userid)
	if (len(tReq) > 1) && (len(tReq[1]) > 1) { // there is a json string ?
		err := json.Unmarshal([]byte(tReq[1]), &spanuattrs)
		if err == nil {
			for attr, val := range spanuattrs {
				if attr != "" {
					span.SetTag("nxt-trace-user-"+attr, val)
				}
			}
		}
		// Spit out the user attribute tags only in the very first span.
		// After that, reset TraceRequestId to have just the request id
		// so that the cpod and connector do not see the json string with
		// user attributes.
		if !flow.ResponseData {
			flow.TraceRequestId = tReq[0]
		}
	}
}

func userGetAttrs(s *zap.SugaredLogger, onboard *nxthdr.NxtOnboard) string {

	usrattr := policy.NxtGetUsrAttr(atype(onboard), onboard.Userid, onboard.Attributes)
	return usrattr
}

func streamFromAgentClose(s *zap.SugaredLogger, MyInfo *shared.Params, tunnel common.Transport,
	dest common.Transport, first bool, Suuid uuid.UUID, onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow, um *userMetrics, span *opentracing.Span, r string) {
	tunnel.Close()
	if dest != nil {
		dest.Close()
	}
	if first {
		// If the very first stream on which we onboarded goes away, then we assume the
		// session goes away
		sessionDel(Suuid)
		if onboard != nil {
			e := agentDel(s, MyInfo, onboard, Suuid)
			if e != nil {
				// TODO: agent delete fail is a bad thing, it can mean that consul entries
				// are hanging around, which is bad for routing! Not sure what should be done
				// if agent del fails
				s.Debugf("Agent del failed", e)
			}
		}
	}
	if span != nil {
		(*span).Finish()
	}
	if flow != nil && um != nil {
		pendingAdd(um, flow)
	}
}

func onboardDiff(sl *zap.SugaredLogger, MyInfo *shared.Params, old *nxthdr.NxtOnboard, new *nxthdr.NxtOnboard) error {
	add := []string{}
	del := []string{}

	for _, o := range old.Services {
		found := false
		for _, n := range new.Services {
			if o == n {
				found = true
				break
			}
		}
		if !found {
			del = append(del, o)
		}
	}

	for _, n := range new.Services {
		found := false
		for _, o := range old.Services {
			if n == o {
				found = true
				break
			}
		}
		if !found {
			add = append(add, n)
		}
	}

	sl.Debugf("Adding-%s and Deleting-%s", add, del)

	for _, s := range add {
		localOneRouteAdd(sl, MyInfo, new, s)
	}
	for _, s := range del {
		localOneRouteDel(sl, MyInfo, new, s)
	}
	if !new.Agent {
		err := consul.RegisterConsul(MyInfo, add, sl)
		if err != nil {
			consul.DeRegisterConsul(MyInfo, add, sl)
			return err
		}
		err = consul.DeRegisterConsul(MyInfo, del, sl)
		if err != nil {
			return err
		}
	}

	old.Services = new.Services

	return nil
}

// This will generate spans for the duration when the packet is on the wire
// (From agent/pod/connector tunnel write() to tunnel read() of this pod
func generateOnWireSpan(spanCtx opentracing.SpanContext, flow *nxthdr.NxtFlow, s *zap.SugaredLogger, tracer opentracing.Tracer) *opentracing.Span {
	if flow.WireSpanStartTime != 0 {
		var startTime = time.Unix(0, int64(flow.WireSpanStartTime))

		if flow.ProcessingTime != 0 {
			var finishTime opentracing.FinishOptions
			var newStartTime = time.Unix(0, int64(flow.ProcessingTime))
			span := tracer.StartSpan("From Agent", opentracing.StartTime(newStartTime))
			finishTime.FinishTime = startTime
			span.FinishWithOptions(finishTime)
			spanCtx = span.Context()
		}
		span := wireTracer.StartSpan("On Wire", opentracing.StartTime(startTime), opentracing.FollowsFrom(spanCtx))
		span.Finish()
		return &span
	}
	return nil
}

func traceAttrListJson(s *zap.SugaredLogger, userattrs string, traceattrs []string) string {
	uattr := make(map[string]interface{})
	truattr := make(map[string]interface{})
	err := json.Unmarshal([]byte(userattrs), &uattr)
	if err != nil {
		s.Errorf("traceAttrListJson: user attributes unmarshal error - %v", err)
		return ""
	}
	if (traceattrs[0] == "all") || (traceattrs[0] == "*") {
		// get all user attributes
		for k, val := range uattr {
			truattr[k] = val
		}
	} else {
		// get specified valid user attributes
		for _, attr := range traceattrs {
			val, ok := uattr[attr]
			if ok {
				truattr[attr] = val
			}
		}
	}
	jsonlist, merr := json.Marshal(truattr)
	if merr != nil {
		s.Errorf("traceAttrListJson: trace attributes list marshal error - %v", merr)
	}
	return string(jsonlist)
}

func traceAgentFlow(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow) *opentracing.Span {
	if onboard.Agent {
		var attrs []string
		var tracereq string
		treqinfo := make(map[string][]string)
		// If apod, check if we need to trace this flow
		traceresult := policy.NxtTraceLookup(atype(onboard), flow.Usrattr)
		s.Debugf("traceAgentFlow: traceresult = %v", traceresult)
		// traceresult can be in one of two forms :
		// 1) {<requestId>: [<optional list of user attributes as span tags]}
		//    Note: if <requestid> is "no" or "none", user attribute list is
		//          don't care (should be empty)
		// 2) <requestid>  (this is for backward compatibility)
		err := json.Unmarshal([]byte(traceresult), &treqinfo)
		if err != nil {
			s.Debugf("traceAgentFlow: trace result unmarshal error %v", err)
			// Must be original format where policy returns just requestid
			err = json.Unmarshal([]byte(traceresult), &tracereq)
			treqinfo[tracereq] = []string{""}
		}
		for tracereq, attrs = range treqinfo {
			if (tracereq == "no") || (tracereq == "none") {
				s.Debugf("traceAgentFlow: %s trace request matched for this flow", tracereq)
				return nil
			}
			flow.TraceRequestId = tracereq + ": " + traceAttrListJson(s, flow.Usrattr, attrs)
			break
		}
		// some non-default trace request returned, so trace this flow
		tracer := opentracing.GlobalTracer()
		wSpan := generateOnWireSpan(nil, flow, s, tracer)
		// Reset the processing time
		flow.ProcessingTime = 0
		span := tracer.StartSpan(MyInfo.Id+"-"+MyInfo.Host, opentracing.FollowsFrom((*wSpan).Context()))
		ext.SpanKindRPCClient.Set(span)
		ext.HTTPUrl.Set(span, "http://"+flow.Dest)
		ext.HTTPMethod.Set(span, "PUT")
		// Info to be propagated downstream up to Connector
		return &span
	} else {
		var span opentracing.Span
		// cpod. Check if this is the return for a traced flow
		if flow.TraceCtx == "" {
			return nil
		}
		// Create a dummy "uber-trace-id" http header.
		// Then use it to create a spanCtx
		httpHdr := make(http.Header)
		httpHdr.Add("Uber-Trace-Id", flow.TraceCtx)
		tracer := opentracing.GlobalTracer()
		spanCtx, serr := tracer.Extract(opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(httpHdr))
		if (serr != nil) || (spanCtx == nil) {
			return nil
		}
		// Create a dummy span while the packet is on the wire from write() of the agent/connector
		// to tunnel.read() of this pod
		wSpan := generateOnWireSpan(spanCtx, flow, s, tracer)
		// Note: Call to wSpan.Context() is still valid after wSpan.Finish() according to docs.
		if wSpan != nil {
			span = tracer.StartSpan(MyInfo.Id+"-"+MyInfo.Host, opentracing.FollowsFrom((*wSpan).Context()))
		} else {
			span = tracer.StartSpan(MyInfo.Id+"-"+MyInfo.Host, opentracing.FollowsFrom(spanCtx))
		}
		span.Tracer().Inject(
			span.Context(),
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(httpHdr),
		)
		flow.TraceCtx = httpHdr.Get("Uber-Trace-Id")
		return &span
	}
}

func generateAgentTrace(trace *nxthdr.NxtTrace, drift int64) {
	tracer := opentracing.GlobalTracer()
	hhdr := make(http.Header)
	hhdr.Add("Uber-Trace-Id", trace.TraceCtx)
	spanCtx, _ := tracer.Extract(opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(hhdr))

	var tNow = uint64(time.Now().UnixNano())
	var onWireDuration = tNow - trace.ProcessingTime

	var span opentracing.Span
	var finishTime opentracing.FinishOptions
	var startTime = time.Unix(0, int64(trace.WireSpanStartTime)+drift)
	var eTime = time.Unix(0, int64(trace.ProcessingTime)+drift)
	var onWireStart = time.Unix(0, int64(trace.WireSpanStartTime-onWireDuration)+drift)

	// Create the "On wire" span based on the times provided by Agent in the ctrl packet
	span = wireTracer.StartSpan("On wire to Agent", opentracing.StartTime(onWireStart), opentracing.FollowsFrom(spanCtx))
	finishTime.FinishTime = startTime
	span.FinishWithOptions(finishTime)

	spanCtx = span.Context()

	// Create the Agent span for processing the packet from Cluster.
	span = tracer.StartSpan("Agent", opentracing.StartTime(startTime), opentracing.FollowsFrom(spanCtx))
	finishTime.FinishTime = eTime
	span.FinishWithOptions(finishTime)
}

// Agent/Connector is trying to connect to minion. The first stream from the agent/connector
// will be used to onboard AND send L3 data. The next streams on the session will not need
// onboarding, and they will send L4/Proxy data. There will be one stream per L4/Proxy session,
// there will be just one stream (first stream) for all L3 data.
//
// NOTE: The Suuid (Session UUID) is purely a way to know that "these are all the streams belonging
// to the same session". When a session from an agent is torn down and recreated, the Suuid will change.
// That is, even though the agent/end point is the same, each time its big session (carrying streams)
// is torn down and recreated, the Suuid will change. Now how is this info useful ? We just use this
// to associate onboarding info with a session - onboarding is done just once per session, the next
// set of streams on the session dont have to onboard. But if the session from the agent goes down and
// the agent creates a new session, it has to onboard all over again. And note that the Suuid is NOT
// something sent by the agent itself, its something generated by the common.Transport library, so this
// value is different from any other uuid etc.. that the agent itself might send
func streamFromAgent(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport, httphdrs *http.Header) {
	var first bool = true
	var dest common.Transport
	var bundle *nxthdr.NxtOnboard
	var userAttr string
	var span *opentracing.Span
	var um *userMetrics
	var lastFlow *nxthdr.NxtFlow
	var fm *flowMetrics

	onboard := sessionGet(Suuid)
	if onboard != nil {
		// this means we are not the first stream in this session
		first = false
	}

	s.Debugf("New agent stream %s : %v, goroutines %d", Suuid, onboard, totGoroutines)

	for {
		hdr, agentBuf, err := tunnel.Read()
		if err != nil {
			streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "")
			s.Debugf("Agent read error - %v", err)
			return
		}
		length := 0
		for _, b := range agentBuf {
			length += len(b)
		}

		switch hdr.Hdr.(type) {
		case *nxthdr.NxtHdr_Trace:
			// Ctrl packet for agent tracing support
			trace := hdr.Hdr.(*nxthdr.NxtHdr_Trace).Trace
			// Drift is in nanoseconds
			s.Debugf("Got trace", trace, tunnel.Timing().Drift, tunnel.Timing().Rtt)
			generateAgentTrace(trace, tunnel.Timing().Drift)

		case *nxthdr.NxtHdr_Onboard:
			if onboard == nil {
				onboard = hdr.Hdr.(*nxthdr.NxtHdr_Onboard).Onboard
				sessionAdd(Suuid, onboard)
				if e := agentAdd(s, MyInfo, onboard, Suuid, tunnel); e != nil {
					s.Debugf("Agent add failed, closing tunnels - %v", e)
					streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "")
					return
				}
			} else {
				newOnb := hdr.Hdr.(*nxthdr.NxtHdr_Onboard).Onboard
				if e := onboardDiff(s, MyInfo, onboard, newOnb); e != nil {
					s.Debugf("Onboard diff failed, closing tunnels - %v", e)
					streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "")
					return
				}
			}
			// The handshake is that we get onboard info and we send back an onboard info
			// with data that agent might need
			onboard.JaegerCollector = MyInfo.JaegerCollector
			err := tunnel.Write(hdr, agentBuf)
			if err != nil {
				s.Debugf("Handshake failed")
			}

		case *nxthdr.NxtHdr_Flow:
			flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
			lastFlow = flow
			if flow.Type == nxthdr.NxtFlow_L3 {
				s.Debugf("Not expecting anything other than L4 flows at this time")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "Agent not onboarded")
				return
			}
			if first {
				s.Debugf("We dont expect L4 flows on the first stream")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "Agent not onboarded")
				return
			}
			if onboard == nil {
				s.Debugf("Agent not onboarded yet")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "Agent not onboarded")
				return
			}
			// Indicate to the destination connector which exact agent/connector is originating this flow,
			// so we can find the right agent/connector in the return path.
			if !flow.ResponseData {
				flow.AgentUuid = onboard.Userid + ":" + onboard.Uuid
				flow.UserCluster = MyInfo.Id
				flow.UserPod = MyInfo.Host
			}
			if userAttr == "" {
				userAttr = userGetAttrs(s, onboard)
			}

			flow.Usrattr = userAttr
			flow.Userid = onboard.Userid
			// Route lookup just one time
			if dest == nil {
				span = traceAgentFlow(s, MyInfo, onboard, flow)
				um, fm = metricFlowAdd(s, flow)
				bundle, dest = globalRouteLookup(s, MyInfo, ctx, onboard, flow, span)
				// L4 routing failures need to terminate the flow
				if dest == nil {
					s.Debugf("Agent flow dest fail: %v", flow)
					streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard,
						lastFlow, um, span, "Couldn't get destination pod or open stream to it")
					return
				}
				s.Debugf("Agent L4 Lookup: flow=%v bundle=%v stream=%v", flow, bundle, dest)
				// If the destination (Tx) closes, close the rx also so the entire goroutine exits and
				// the close is cascaded to the other elements connected to the cluster (pods/agents)
				dest.CloseCascade(tunnel)
			}
			var finishT int64
			if span != nil {
				finishT = time.Now().UnixNano()
				flow.WireSpanStartTime = uint64(finishT)
			}
			err := dest.Write(hdr, agentBuf)
			if err != nil {
				s.Debugf("Agent flow write fail: flow=%v, error=%v", flow, err)
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, lastFlow, um, span, "Write to Agent failed")
				return
			}
			if span != nil {
				var finishTime opentracing.FinishOptions
				finishTime.FinishTime = time.Unix(0, finishT)
				(*span).FinishWithOptions(finishTime)
				span = nil
			}
			if fm != nil {
				incrMetrics(fm, length)
			}
		}
	}
}

func bundleAccessAllowed(s *zap.SugaredLogger, flow *nxthdr.NxtFlow, onboard *nxthdr.NxtOnboard) bool {
	// The AAA access check is done per packet today, we can save some performance if we
	// do it once per flow like route lookup, but then we lose the ability to firewall the
	// flow after its established
	if !policy.NxtAccessOk(atype(onboard), (onboard).Userid, flow.Usrattr) {
		return false
	}
	// Going to an agent/connector, we dont need the attributes anymore
	flow.Usrattr = ""

	// Going to an agent, we dont need to carry around the identity of the unique user device
	// originating the flow
	if onboard.Agent {
		flow.AgentUuid = ""
	}
	return true
}

func streamFromPodClose(s *zap.SugaredLogger, tunnel common.Transport, dest common.Transport, MyInfo *shared.Params, flow *nxthdr.NxtFlow, um *userMetrics, span *opentracing.Span, r string) {
	tunnel.Close()
	if dest != nil {
		dest.Close()
	}
	if span != nil {
		(*span).Finish()
	}
	if flow != nil && um != nil {
		pendingAdd(um, flow)
	}
}

func traceInterpodFlow(s *zap.SugaredLogger, MyInfo *shared.Params, flow *nxthdr.NxtFlow) *opentracing.Span {
	var span opentracing.Span
	if flow.TraceCtx == "" {
		return nil
	}
	tracer := opentracing.GlobalTracer()
	hhdr := make(http.Header)
	hhdr.Add("Uber-Trace-Id", flow.TraceCtx)
	spanCtx, serr := tracer.Extract(opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(hhdr))
	if (serr != nil) || (spanCtx == nil) {
		return nil
	}
	// Create a dummy span while the packet is on the wire from dest.write() of the previous
	// pod to tunnel.read() on this pod.
	wSpan := generateOnWireSpan(spanCtx, flow, s, tracer)
	// Note: Call to wSpan.Context() below is still valid after wSpan.Finish() according to Jaeger trace docs.
	if wSpan != nil {
		span = tracer.StartSpan(MyInfo.Id+"-"+MyInfo.Host, opentracing.FollowsFrom((*wSpan).Context()))
	} else {
		span = tracer.StartSpan(MyInfo.Id+"-"+MyInfo.Host, opentracing.FollowsFrom(spanCtx))
	}
	setSpanTags(span, flow, MyInfo)
	span.Tracer().Inject(
		span.Context(),
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(hhdr),
	)
	flow.TraceCtx = hhdr.Get("Uber-Trace-Id")
	s.Debugf("Trace: Found span context in stream from %s to %s", flow.Userid, flow.DestAgent)
	return &span
}

// Handle data that arrives from other pods, the other pod can be on the local cluster or it might be on
// a remote cluster. Basically the other pod did a route lookup and found that this pod (where this API runs)
// is hosting the agent/connector of its choice, so as far as this API is concerned, its destination stream
// is always local.
// NOTE: The "Suuid" is supposed to be the "session uuid", ie there can be one session uniquely identified
// by it and many streams under that session. But not all transport types offer the ability to identify a
// session uniquely, for example http2 does not (see comments in httpHandler in common repo http2.go). So
// before using Suuid see if the underlying transport supports it
func streamFromPod(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport, httphdrs *http.Header) {
	var dest common.Transport
	var onboard *nxthdr.NxtOnboard
	var span *opentracing.Span
	var um *userMetrics
	var fm *flowMetrics
	var lastFlow *nxthdr.NxtFlow

	s.Debugf("New interpod HTTP stream %v - %v", Suuid, httphdrs)

	for {
		hdr, podBuf, err := tunnel.Read()
		if err != nil {
			s.Debugf("InterPod Error", err)
			streamFromPodClose(s, tunnel, dest, MyInfo, lastFlow, um, span, "")
			return
		}
		length := 0
		for _, b := range podBuf {
			length += len(b)
		}

		switch hdr.Hdr.(type) {
		case *nxthdr.NxtHdr_Flow:
			flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
			lastFlow = flow
			if flow.Type == nxthdr.NxtFlow_L3 {
				panic("Not expecting anything other than L4 flows at this time")
			}
			// Route lookup just one time
			if dest == nil {
				span = traceInterpodFlow(s, MyInfo, flow)
				um, fm = metricFlowAdd(s, flow)
				onboard, dest = localRouteLookup(s, MyInfo, flow)
				if dest == nil {
					s.Debugf("Interpod: cant get dest tunnel for ", flow.DestAgent)
					streamFromPodClose(s, tunnel, dest, MyInfo, lastFlow, um, span, "Couldn't get destination to agent")
					return
				}
				s.Debugf("Interpod L4 Lookup", flow, onboard, dest)
				dest = dest.NewStream(nil)
				if dest == nil {
					s.Debugf("Interpod: cant open stream on dest tunnel for ", flow.DestAgent)
					streamFromPodClose(s, tunnel, dest, MyInfo, lastFlow, um, span, "Stream open to agent failed")
					return
				}
				// If the destination (Tx) closes, close the rx also so the entire goroutine exits and
				// the close is cascaded to the other elements connected to the cluster (pods/agents)
				dest.CloseCascade(tunnel)
			}
			if !bundleAccessAllowed(s, flow, onboard) {
				streamFromPodClose(s, tunnel, dest, MyInfo, lastFlow, um, span, "Agent access denied")
				return
			}
			var finishT int64
			if span != nil {
				finishT = time.Now().UnixNano()
				flow.WireSpanStartTime = uint64(finishT)
			}
			err := dest.Write(hdr, podBuf)

			if err != nil {
				s.Debugf("Interpod: l4 dest write failed ", flow.DestAgent, err)
				streamFromPodClose(s, tunnel, dest, MyInfo, lastFlow, um, span, "Write to destination pod failed")
				return
			}
			if span != nil {
				var finishTime opentracing.FinishOptions
				finishTime.FinishTime = time.Unix(0, finishT)
				(*span).FinishWithOptions(finishTime)
				span = nil
			}
			if fm != nil {
				incrMetrics(fm, length)
			}
		}
	}
}

func RouterInit(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	sessions = make(map[uuid.UUID]nxthdr.NxtOnboard)
	agents = make(map[string]*agentTunnel)
	users = make(map[string][]*agentTunnel)
	pods = make(map[string]*podInfo)
	metrics = make(map[string]*userMetrics)
	pendingFree = make([]*deleteMetrics, 0)
	localRoutes = make(map[string]*nxthdr.NxtOnboard)
	unusedChan = make(chan common.NxtStream)
	outsideMsg = make(chan bool)
	insideMsg = make(chan bool)

	go outsideListener(s, MyInfo, ctx, "websocket")
	go insideListener(s, MyInfo, ctx, "http2")
	go healthCheck(s, MyInfo, ctx)
	go metricsHandler()
	go garbageCollectFlows(s)
	// For apods, inside/outside is always open. For cpod, outside is open only
	// if there is no connector connected to it yet, and inside is open only if
	// there is a connector connected as of now. In other words, a cpod will accept
	// only one outside connection (connection from a connector) and a cpod will
	// accept inside connection (from an apod) only if there is a connector connected
	if MyInfo.PodType == "apod" {
		outsideMsg <- true
		insideMsg <- true
	} else {
		// accept a connection, and when a connection is established, it will ensure
		// to send an insideMsg <- true at that point
		outsideMsg <- true
		insideMsg <- false
	}
}

func InitJaegerTrace(service string, MyInfo *shared.Params, s *zap.SugaredLogger, onWire bool) io.Closer {
	var tracer opentracing.Tracer
	var closer io.Closer
	var err error

	cfg := &jaegercfg.Configuration{
		ServiceName: service,
		Sampler: &jaegercfg.SamplerConfig{
			Type:  "const",
			Param: 1,
		},
		Reporter: &jaegercfg.ReporterConfig{
			LogSpans: true,
		},
	}
	jLogger := jaegerlog.StdLogger
	debugLogger := jaegerlog.DebugLogAdapter(jLogger)
	jMetricsFactory := jaegermetrics.New()
	if onWire {
		tracer, closer, err = cfg.NewTracer(jaegercfg.Logger(debugLogger))
	} else {
		tracer, closer, err = cfg.NewTracer(
			jaegercfg.Logger(debugLogger),
			jaegercfg.Metrics(jMetricsFactory),
		)
	}
	if err != nil {
		s.Errorf("JaegerTraceInit Failed - %v", err)
		cfg.Disabled = true
		tracer, closer, _ = cfg.NewTracer()
	}
	if onWire {
		wireTracer = tracer
	} else {
		opentracing.SetGlobalTracer(tracer)
	}
	return closer
}

// Open a Websocket server side socket and listen for incoming connections
// from agents, and for each agent connection, spawn a goroutine to handle that
func outsideListenerWebsocket(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	var pvtKey, pubKey []byte
	lg := log.New(&zap2log{s: s}, "websock", 0)
	var server *websock.WebStream = nil
	tchan := make(chan common.NxtStream)
	var closer io.Closer

	if MyInfo.PodType == "apod" {
		closer = InitJaegerTrace(MyInfo.Namespace+"-trace", MyInfo, s, false)
		if closer != nil {
			defer closer.Close()
		}
	}
	for {
		select {
		case client := <-tchan:
			go streamFromAgent(s, MyInfo, ctx, client.Parent, client.Stream, client.Http)
		case open := <-outsideMsg:
			if open {
				if server == nil {
					if MyInfo.PodType == "apod" {
						// We dont want any keepalives from umpteen agents, we do clock sync with agents
						server = websock.NewListener(ctx, lg, pvtKey, pubKey, MyInfo.Oport, 0, 0, 500)
					} else {
						// as for a keepalive count of at least one data activity in 30 seconds
						server = websock.NewListener(ctx, lg, pvtKey, pubKey, MyInfo.Oport, 30*1000, 1, 500)
					}
					go server.Listen(tchan)
				}
			} else {
				if server != nil {
					s.Debugf("Closing websocket server")
					server.Close()
					server = nil
				}
			}
		}
	}
}

// Open an http2 socket to listen for incoming connections from other pods in the
// same cluster or other pods from outside this cluster
func insideListenerHttp2(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	var pvtKey []byte
	var pubKey []byte
	lg := log.New(&zap2log{s: s}, "http2", 0)
	var server *nhttp2.HttpStream = nil
	tchan := make(chan common.NxtStream)
	var closer io.Closer

	if MyInfo.PodType != "apod" {
		closer = InitJaegerTrace(MyInfo.Namespace+"-trace", MyInfo, s, false)
		if closer != nil {
			defer closer.Close()
		}
	}
	for {
		select {
		case client := <-tchan:
			go streamFromPod(s, MyInfo, ctx, client.Parent, client.Stream, client.Http)
		case open := <-insideMsg:
			if open {
				if server == nil {
					server = nhttp2.NewListener(ctx, lg, pvtKey, pubKey, MyInfo.Iport, &totGoroutines)
					go server.Listen(tchan)
					insideOpen = true
				}
			} else {
				if server != nil {
					s.Debugf("Closing http2 server")
					server.Close()
					server = nil
					insideOpen = false
				}
			}
		}
	}
}

// Inside listener is today http2 based. We can have options in future where for example we have
// http3 as an option
func insideListener(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, encap string) {
	if encap == "http2" {
		insideListenerHttp2(s, MyInfo, ctx)
	} else {
		// Unknown encap
		panic(encap)
	}
}

// The nextensio/common package defines various encap types and we expect this list to
// eventually grow to support different encaps
func outsideListener(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, encap string) {
	if encap == "websocket" {
		outsideListenerWebsocket(s, MyInfo, ctx)
	} else {
		// Unknown encap
		panic(encap)
	}
}

func healthHandler(s *zap.SugaredLogger, w http.ResponseWriter, r *http.Request) {
	if insideOpen {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("NotOK"))
	}
}

func healthCheck(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		healthHandler(s, w, r)
	})
	addr := ":" + strconv.Itoa(MyInfo.HealthPort)
	server := http.Server{
		Addr: addr, Handler: mux,
	}
	server.ListenAndServe()
}

func metricsHandler() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	addr := ":" + strconv.Itoa(8888)
	server := http.Server{
		Addr: addr, Handler: mux,
	}
	server.ListenAndServe()
}

func DumpInfo(s *zap.SugaredLogger) {
	aLock.RLock()
	s.Debugf("Total Sessions: ", len(sessions))
	s.Debugf("Total Users: ", len(users))
	s.Debugf("Total agents: ", len(agents))
	aLock.Unlock()

	routeLock.RLock()
	s.Debugf("Total local routes: ", len(localRoutes))
	routeLock.Unlock()

	pLock.RLock()
	s.Debugf("Total pods: ", len(pods))
	pLock.Unlock()

	mLock.RLock()
	s.Debugf("Total prometheus counters: ", len(metrics))
	mLock.Unlock()

	pendingLock.Lock()
	s.Debugf("Total pending free flows: ", len(pendingFree))
	pendingLock.Unlock()
}
