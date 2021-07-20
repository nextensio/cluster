package router

import (
	"bytes"
	"context"
	"fmt"
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
	"minion.io/aaa"
	"minion.io/consul"
	"minion.io/shared"

	"github.com/google/uuid"
)

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

type dropInfo struct {
	reason string
	flow   *nxthdr.NxtFlow
	myInfo *shared.Params
}

type agentTunnel struct {
	tunnel common.Transport
	suuid  uuid.UUID
}

// TODO: The lock is a big fat lock here and is liberally taken during the entire duration
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
var pLock sync.RWMutex
var pods map[string]*podInfo
var unusedChan chan common.NxtStream
var outsideMsg chan bool
var insideMsg chan bool
var routeLock sync.RWMutex
var localRoutes map[string]*nxthdr.NxtOnboard
var pakdropReq chan dropInfo
var totGoroutines int32
var insideOpen bool

// NOTE: About all the multitude of UUIDs.
// onboard.userid: This everyone understands - its like foobar@nextensio.net
//
// onboard.Uuid: foobar@nextensio.net can have three devices, one laptop, one
// ipad and one android phone. So when foobar connects to nextensio from all these devices,
// each device will get a unique id, wihch is onboard.Uuid
//
// Suuid: Now foobar conneting from laptop might have multiple tunnels to the cluster - either
// because we support multiple tunnels from the same device (eventually, not yet), or it can be
// transient situations where one tunnel is going down and not cleaned up fully while the other
// tunnel is coming up etc.. - this can very well happen because each tunnel is handled by
// independent goroutines. Suuid means 'session UUid'

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

	if !aaa.UsrAllowed(atype(onboard), onboard.Userid, onboard.Cluster, onboard.Podname, s) {
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
		aaa.UsrLeave(atype(onboard), onboard.Userid, s)
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

// Passed as callback when calling AaaStart() from minion.go
func DisconnectUser(userid string, s *zap.SugaredLogger) {
	aLock.RLock()
	defer aLock.RUnlock()

	cur := users[userid]
	var i = 0
	for ; i < len(cur); i++ {
		cur[i].tunnel.Close()
	}

	s.Debugf("Force disconnected user %s, tunnels %d", userid, i)
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
	flow := nxthdr.NxtFlow{}
	hdr.Hdr = &nxthdr.NxtHdr_Flow{Flow: &flow}
	hdr.Streamop = nxthdr.NxtHdr_KEEP_ALIVE
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

func localRouteAdd(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	// Register to the local routing table so that we know what services are
	// available on this pod.
	for _, s := range onboard.Services {
		s = strings.ReplaceAll(s, ".", "-")
		// Multiple agents can login with the same userid, each agent tunnel has a unique uuid
		if onboard.Agent {
			s = s + onboard.Uuid
		}
		routeLock.Lock()
		localRoutes[s] = onboard
		routeLock.Unlock()
	}
}

func localRouteDel(sl *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	for _, s := range onboard.Services {
		s = strings.ReplaceAll(s, ".", "-")
		// Multiple agents can login with the same userid, each agent tunnel has a unique uuid
		if onboard.Agent {
			s = s + onboard.Uuid
		}
		routeLock.Lock()
		localRoutes[s] = nil
		routeLock.Unlock()
	}
}

// Lookup destination stream to send an L4 tcp/udp/http proxy packet on
func globalRouteLookup(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context,
	onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow) (*nxthdr.NxtOnboard, common.Transport) {
	var consul_key, tag string
	var fwd shared.Fwd
	var err error

	// Do a Consul DNS lookup for the user -> connector direction only
	// For the reply connector -> user direction, the target cluster and pod are
	// obtained from the flow header fields UserCluster and UserPod. UserPod is set
	// as the value of the x-nextensio-for header.
	if !flow.ResponseData {
		// We're on an Apod, trying to send the frame to a Cpod
		tag = aaa.RouteLookup(atype(onboard), onboard.Userid, flow.DestAgent, s)
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

func userAccessAllowed(s *zap.SugaredLogger, flow *nxthdr.NxtFlow, onboard *nxthdr.NxtOnboard, bundle *nxthdr.NxtOnboard) bool {
	usrattr, attrok := aaa.GetUsrAttr(atype(onboard), onboard.Userid, s)
	if attrok {
		flow.Usrattr = usrattr
	}
	// TODO: This check is not really needed as it's for DestType == SelfDest, ie., for any
	// traffic between user and connector within the same pod.
	// Users and connectors cannot connect to the same pod as per POR.
	if bundle != nil {
		if !aaa.AccessOk(atype(bundle), (*bundle).Userid, flow.Usrattr, s) {
			return false
		}
		// Going to another agent/connector, we dont need the attributes anymore
		flow.Usrattr = ""
	}

	return true
}

func streamFromAgentClose(s *zap.SugaredLogger, MyInfo *shared.Params, tunnel common.Transport,
	dest common.Transport, first bool, Suuid uuid.UUID, onboard *nxthdr.NxtOnboard, f *nxthdr.NxtFlow, r string) {
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
	if (r != "") && (f != nil) {
		pakdropReq <- dropInfo{reason: r, flow: f, myInfo: MyInfo}
	}
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
func streamFromAgent(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport, http *http.Header) {
	var first bool = true
	var dest common.Transport
	var bundle *nxthdr.NxtOnboard

	onboard := sessionGet(Suuid)
	if onboard != nil {
		// this means we are not the first stream in this session
		first = false
	}

	s.Debugf("New agent stream %s : %v, goroutines %d", Suuid, onboard, totGoroutines)

	for {
		hdr, agentBuf, err := tunnel.Read()
		if err != nil {
			streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, nil, "")
			s.Debugf("Agent read error - %v", err)
			return
		}

		switch hdr.Hdr.(type) {
		case *nxthdr.NxtHdr_Onboard:
			// The handshake is that we get onboard info and we write it back. This is only
			// if the transport is non reliable like dtls, so the other end knows we received
			// the onboard info. We should not have to do this for reliable transport
			err := tunnel.Write(hdr, agentBuf)
			if err != nil {
				s.Debugf("Handshake failed")
			}
			if onboard == nil {
				onboard = hdr.Hdr.(*nxthdr.NxtHdr_Onboard).Onboard
				sessionAdd(Suuid, onboard)
				if e := agentAdd(s, MyInfo, onboard, Suuid, tunnel); e != nil {
					s.Debugf("Agent add failed, closing tunnels - %v", e)
					streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, nil, "")
					return
				}
			}

		case *nxthdr.NxtHdr_Flow:
			flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
			if flow.Type == nxthdr.NxtFlow_L3 {
				s.Debugf("Not expecting anything other than L4 flows at this time")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, flow, "Agent not onboarded")
				return
			}
			if first {
				s.Debugf("We dont expect L4 flows on the first stream")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, flow, "Agent not onboarded")
				return
			}
			if onboard == nil {
				s.Debugf("Agent not onboarded yet")
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, flow, "Agent not onboarded")
				return
			}
			// Indicate to the destination connector which exact agent/connector is originating this flow,
			// so we can find the right agent/connector in the return path.
			if !flow.ResponseData {
				flow.AgentUuid = onboard.Uuid
				flow.UserCluster = MyInfo.Id
				flow.UserPod = MyInfo.Host
			}
			// Route lookup just one time
			if dest == nil {
				bundle, dest = globalRouteLookup(s, MyInfo, ctx, onboard, flow)
				// L4 routing failures need to terminate the flow
				if dest == nil {
					s.Debugf("Agent flow dest fail: %v", flow)
					streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard,
						flow, "Couldn't get destination pod or open stream to it")
					return
				}
				s.Debugf("Agent L4 Lookup: flow=%v bundle=%v stream=%v", flow, bundle, dest)
				// If the destination (Tx) closes, close the rx also so the entire goroutine exits and
				// the close is cascaded to the other elements connected to the cluster (pods/agents)
				dest.CloseCascade(tunnel)
			}
			if !userAccessAllowed(s, flow, onboard, bundle) {
				s.Debugf("Agent access fail: bundle=%v DestAgent=%s", bundle, flow.DestAgent)
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, flow, "Agent access denied")
				return
			}
			err := dest.Write(hdr, agentBuf)
			if err != nil {
				s.Debugf("Agent flow write fail: flow=%v, error=%v", flow, err)
				streamFromAgentClose(s, MyInfo, tunnel, dest, first, Suuid, onboard, flow, "Write to Agent failed")
				return
			}
		}
	}
}

func bundleAccessAllowed(s *zap.SugaredLogger, flow *nxthdr.NxtFlow, onboard *nxthdr.NxtOnboard) bool {
	// The AAA access check is done per packet today, we can save some performance if we
	// do it once per flow like route lookup, but then we lose the ability to firewall the
	// flow after its established
	if !aaa.AccessOk(atype(onboard), (onboard).Userid, flow.Usrattr, s) {
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

var nullConn net.Conn = nil

func pakDrop(s *zap.SugaredLogger) {
	for {
		select {
		case info := <-pakdropReq:
			s.Debugf("Dropping frame from %s - %s", info.flow.SourceAgent, info.reason)
			continue // until blackhole support is in

			if nullConn == nil {
				servAddr := strings.Join([]string{"blackhole", strconv.Itoa(10000)}, ":")
				conn, e := net.Dial("tcp", servAddr)
				if e != nil {
					continue
				}
				nullConn = conn
			}
			reqhdr := []byte("GET / HTTP/1.0")
			drophdr := []byte("x-nextensio-drop: " + info.reason)
			// SourceAgentID: NxtFlow.SourceAgent
			sahdr := []byte("x-nextensio-sourceagent: " + info.flow.SourceAgent)
			// SourceClusterID: MyInfo.Id
			schdr := []byte("x-nextensio-sourcecluster: " + info.myInfo.Id)
			// SourcePodID: MyInfo.Host
			sphdr := []byte("x-nextensio-sourcepod: " + info.myInfo.Host)
			clenhdr := []byte("Content-length: 0\r\n\r\n")
			pak := bytes.Join([][]byte{reqhdr, drophdr, sahdr, schdr, sphdr, clenhdr},
				[]byte("\r\n"))
			_, e := nullConn.Write(pak)
			if e != nil {
				nullConn.Close()
				nullConn = nil
			}
		}
	}
}

func streamFromPodClose(tunnel common.Transport, dest common.Transport, MyInfo *shared.Params, f *nxthdr.NxtFlow, r string) {
	tunnel.Close()
	if dest != nil {
		dest.Close()
	}
	if (r != "") && (f != nil) {
		pakdropReq <- dropInfo{reason: r, flow: f, myInfo: MyInfo}
	}
}

// Handle data that arrives from other pods, the other pod can be on the local cluster or it might be on
// a remote cluster. Basically the other pod did a route lookup and found that this pod (where this API runs)
// is hosting the agent/connector of its choice, so as far as this API is concerned, its destination stream
// is always local.
// NOTE: The "Suuid" is supposed to be the "session uuid", ie there can be one session uniquely identified
// by it and many streams under that session. But not all transport types offer the ability to identify a
// session uniquely, for example http2 does not (see comments in httpHandler in common repo http2.go). So
// before using Suuid see if the underlying transport supports it
func streamFromPod(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport, http *http.Header) {
	var dest common.Transport
	var onboard *nxthdr.NxtOnboard

	s.Debugf("New interpod stream", Suuid)

	for {
		hdr, podBuf, err := tunnel.Read()
		if err != nil {
			s.Debugf("InterPod Error", err)
			streamFromPodClose(tunnel, dest, MyInfo, nil, "")
			return
		}
		if hdr.Streamop == nxthdr.NxtHdr_KEEP_ALIVE {
			// nothing to do
		} else {
			switch hdr.Hdr.(type) {
			case *nxthdr.NxtHdr_Flow:
				flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
				if flow.Type == nxthdr.NxtFlow_L3 {
					panic("Not expecting anything other than L4 flows at this time")
				}
				// Route lookup just one time
				if dest == nil {
					onboard, dest = localRouteLookup(s, MyInfo, flow)
					if dest == nil {
						s.Debugf("Interpod: cant get dest tunnel for ", flow.DestAgent)
						streamFromPodClose(tunnel, dest, MyInfo, flow, "Couldn't get destination to agent")
						return
					}
					s.Debugf("Interpod L4 Lookup", flow, onboard, dest)
					dest = dest.NewStream(nil)
					if dest == nil {
						s.Debugf("Interpod: cant open stream on dest tunnel for ", flow.DestAgent)
						streamFromPodClose(tunnel, dest, MyInfo, flow, "Stream open to agent failed")
						return
					}
					// If the destination (Tx) closes, close the rx also so the entire goroutine exits and
					// the close is cascaded to the other elements connected to the cluster (pods/agents)
					dest.CloseCascade(tunnel)
				}
				if !bundleAccessAllowed(s, flow, onboard) {
					streamFromPodClose(tunnel, dest, MyInfo, flow, "Agent access denied")
					return
				}
				err := dest.Write(hdr, podBuf)
				if err != nil {
					s.Debugf("Interpod: l4 dest write failed ", flow.DestAgent, err)
					streamFromPodClose(tunnel, dest, MyInfo, flow, "Write to destination pod failed")
					return
				}
			}
		}
	}
}

func RouterInit(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	sessions = make(map[uuid.UUID]nxthdr.NxtOnboard)
	agents = make(map[string]*agentTunnel)
	users = make(map[string][]*agentTunnel)
	pods = make(map[string]*podInfo)
	localRoutes = make(map[string]*nxthdr.NxtOnboard)
	unusedChan = make(chan common.NxtStream)
	outsideMsg = make(chan bool)
	insideMsg = make(chan bool)
	pakdropReq = make(chan dropInfo)
	go pakDrop(s)
	go outsideListener(s, MyInfo, ctx, "websocket")
	go insideListener(s, MyInfo, ctx, "http2")
	go healthCheck(s, MyInfo, ctx)
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

// Open a Websocket server side socket and listen for incoming connections
// from agents, and for each agent connection, spawn a goroutine to handle that
func outsideListenerWebsocket(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	var pvtKey, pubKey []byte
	lg := log.New(&zap2log{s: s}, "websock", 0)
	var server *websock.WebStream = nil
	tchan := make(chan common.NxtStream)
	for {
		select {
		case client := <-tchan:
			go streamFromAgent(s, MyInfo, ctx, client.Parent, client.Stream, client.Http)
		case open := <-outsideMsg:
			if open {
				if server == nil {
					// as for a keepalive count of at least one data activity in 30 seconds
					server = websock.NewListener(ctx, lg, pvtKey, pubKey, MyInfo.Oport, 30*1000, 1)
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
