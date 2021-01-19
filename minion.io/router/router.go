package router

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"

	"gitlab.com/nextensio/common"
	"gitlab.com/nextensio/common/messages/nxthdr"
	nhttp2 "gitlab.com/nextensio/common/transport/http2"
	websock "gitlab.com/nextensio/common/transport/websocket"
	"go.uber.org/zap"
	"minion.io/aaa"
	"minion.io/consul"
	"minion.io/shared"

	"github.com/google/uuid"
)

type podInfo struct {
	pending bool
	tunnel  common.Transport
}

var aLock sync.RWMutex
var sessions map[uuid.UUID]nxthdr.NxtOnboard
var agents map[string]common.Transport
var pLock sync.RWMutex
var pods map[string]*podInfo
var unusedChan chan common.NxtStream
var routeLock sync.RWMutex
var localRoutes map[string]*nxthdr.NxtOnboard

func sessionAdd(Suuid uuid.UUID, onboard *nxthdr.NxtOnboard) {
	aLock.Lock()
	sessions[Suuid] = *onboard
	aLock.Unlock()
}

func sessionDel(Suuid uuid.UUID) {
	aLock.Lock()
	delete(sessions, Suuid)
	aLock.Unlock()
}

func sessionGet(Suuid uuid.UUID) *nxthdr.NxtOnboard {
	var onboard *nxthdr.NxtOnboard
	aLock.RLock()
	if o, ok := sessions[Suuid]; ok {
		onboard = &o
	}
	aLock.RUnlock()
	return onboard
}

func atype(onboard *nxthdr.NxtOnboard) string {
	if onboard.Agent {
		return "agent"
	} else {
		return "connector"
	}
}

func agentAdd(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, tunnel common.Transport) {
	aLock.Lock()
	agents[onboard.Uuid] = tunnel
	localRouteAdd(s, MyInfo, onboard)
	consul.RegisterConsul(MyInfo, onboard.Services, s)
	aaa.UsrJoin(atype(onboard), onboard.Userid, s)
	aLock.Unlock()
}

func agentDel(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard, tunnel common.Transport) {
	aLock.Lock()
	if agents[onboard.Uuid] == tunnel {
		delete(agents, onboard.Uuid)
		localRouteDel(s, MyInfo, onboard)
		consul.DeRegisterConsul(MyInfo, onboard.Services, s)
		aaa.UsrLeave(atype(onboard), onboard.Userid, s)
	}
	aLock.Unlock()
}

func agentGet(uuid string) common.Transport {
	aLock.RLock()
	tunnel := agents[uuid]
	aLock.RUnlock()

	return tunnel
}

// Lookup a session to the agent/connector on this same pod.
func localLookup(s *zap.SugaredLogger, flow *nxthdr.NxtFlow) (*nxthdr.NxtOnboard, common.Transport) {
	// TODO: This dot dash business has to go away, we have to unify the usage
	// of services everywhere to either dot or dash
	service := strings.ReplaceAll(flow.DestAgent, ".", "-")
	routeLock.RLock()
	onboard := localRoutes[service]
	routeLock.RUnlock()

	if onboard == nil {
		return nil, nil
	}
	tunnel := agentGet(onboard.Uuid)
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
// TODO1: We dont have to create a session to the destination pod + destAgent combo, we really want
// it to go to the destination pod itself and then we can just create streams over that session.
// As of today there is no istio rules installed to redirect a http request to a pod, its only
// redirect-able to an agent, if we install some rule for the pod, we can reduce the number of
// sessions created here. We can do just destination pod for local cluster pod to pod case, but
// just keeping the code same for local and remote cluster at the moment.
// TODO2: These remote pod sessions need to be aged out at some point, will need some reference
// counting / last used timestamp etc..
func podLookup(s *zap.SugaredLogger, ctx context.Context, MyInfo *shared.Params, destAgent string, podIP string) common.Transport {
	var tunnel common.Transport
	var pending bool = false

	key := podIP + destAgent
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
			podDial(s, ctx, MyInfo, destAgent, podIP)
			tunnel = pods[key].tunnel
		} else {
			tunnel = p.tunnel
		}
		pLock.Unlock()
	}

	return tunnel
}

// Create the initial session to a destination pod/remote cluser
func podDial(s *zap.SugaredLogger, ctx context.Context, MyInfo *shared.Params, destAgent string, podIP string) {
	var pubKey []byte
	hdrs := make(http.Header)
	// Take this session to the pod on the remote cluster hosting the particular agent/connector
	hdrs.Add("x-nextensio-for", destAgent)
	// Just a value which lets the other end identify that this is a new "session"
	hdrs.Add("x-nextensio-session", uuid.New().String())
	client := nhttp2.NewClient(ctx, pubKey, podIP, podIP, MyInfo.Iport, hdrs)
	// For pod to pod connectivity, a pod will always dial-out another one,
	// we dont expect a stream to come in from the other end on a dial-out session,
	// and hence the reason we use the unusedChan on which no one is listening.
	// This is a unidirectional session, we just write on this. Writing from the
	// other end will end up with the other end dialling a new connection
	s.Debugf("Dialing pod", destAgent, podIP)
	err := client.Dial(unusedChan)
	if err != nil {
		s.Debugf("Dialing pod error", err, destAgent, podIP)
		return
	}
	s.Debugf("Dialled pod", destAgent, podIP)
	key := podIP + destAgent
	pods[key].pending = false
	pods[key].tunnel = client
}

func localRouteAdd(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	// Register to the local routing table so that we know what services are
	// available on this pod.
	// TODO: handle the case where there are multiple local agents advertising the same service
	// and maybe we want to loadbalance across them ?
	cp := *onboard
	for _, s := range onboard.Services {
		routeLock.Lock()
		localRoutes[s] = &cp
		routeLock.Unlock()
	}
}

func localRouteDel(s *zap.SugaredLogger, MyInfo *shared.Params, onboard *nxthdr.NxtOnboard) {
	// TODO: handle the case where there are multiple local agents advertising the same service
	// and maybe we want to loadbalance across them ? So delete only one agent for that service here
	for _, s := range onboard.Services {
		routeLock.Lock()
		localRoutes[s] = nil
		routeLock.Unlock()
	}
}

func podDelete(key string) {
	pLock.Lock()
	pods[key] = nil
	pLock.Unlock()
}

// Lookup destination stream to send a l3 IP packet on
func l3Lookup(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, onboard *nxthdr.NxtOnboard,
	flow *nxthdr.NxtFlow) (common.Transport, *nxthdr.NxtOnboard, *string) {

	// TODO: Map ip addresses in the packet to service names (savd from dns ?)
	// and do a consul lookup

	// Return fwd.Dest + destAgent as the tunnel key if the pod is a remote pod
	return nil, nil, nil
}

func l3Fwd(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, onboard *nxthdr.NxtOnboard,
	hdr *nxthdr.NxtHdr, agentBuf net.Buffers) {

	flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
	// We do route lookup per packet for L3 flows
	dest, bundle, podKey := l3Lookup(s, MyInfo, ctx, onboard, flow)
	if dest != nil {
		usrattr, attrok := aaa.GetUsrAttr(atype(onboard), onboard.Userid, s)
		if attrok {
			flow.Usrattr = usrattr
		}
		if bundle != nil {
			ok := aaa.AccessOk(atype(bundle), bundle.Userid, flow.Usrattr, s)
			s.Debugf("agent access:", bundle, flow.Usrattr)
			if !ok {
				s.Debugf("agent access failed ", flow.DestAgent)
				return
			}
			// Going to another agent/connector, we dont need the attributes anymore
			flow.Usrattr = ""
		}
		err := dest.Write(hdr, agentBuf)
		if err != nil {
			// L3 routing failures are just fine, packet is dropped and thats it.
			// But if the dest tunnel is non local and it has a write failure, that means
			// something is wrong with the tunnel and we need to remove it from the hash.
			// Usually the goroutine reading from the tunnel will detect error and remove it,
			// but interpod tunnels are unidirectional, no one reads from it on this end
			if podKey != nil {
				podDelete(*podKey)
			}
		}
	}
}

// Lookup destination stream to send an L4 tcp/udp/http proxy packet on
func l4Lookup(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context,
	onboard *nxthdr.NxtOnboard, flow *nxthdr.NxtFlow) (*nxthdr.NxtOnboard, common.Transport) {
	var consul_key, tag string

	tag = aaa.RouteLookup(atype(onboard), onboard.Userid, flow.DestAgent, s)

	host := strings.ReplaceAll(flow.DestAgent, ".", "-")
	if tag == "" {
		consul_key = strings.Join([]string{host, MyInfo.Namespace}, "-")
	} else {
		consul_key = strings.Join([]string{tag, host, MyInfo.Namespace}, "-")
		flow.DestAgent = tag + "." + flow.Dest
	}

	fwd, err := consul.ConsulDnsLookup(MyInfo, consul_key, s)
	if err != nil {
		s.Debugf("Consul lookup failed for dest", flow.Dest)
		return nil, nil
	}

	switch fwd.DestType {
	case shared.SelfDest:
		return localLookup(s, flow)
	case shared.LocalDest:
		return nil, podLookup(s, ctx, MyInfo, flow.DestAgent, fwd.Dest)
	case shared.RemoteDest:
		return nil, podLookup(s, ctx, MyInfo, flow.DestAgent, fwd.Dest)
	}

	return nil, nil
}

func l4Fwd(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, onboard *nxthdr.NxtOnboard,
	hdr *nxthdr.NxtHdr, bundle **nxthdr.NxtOnboard, dest *common.Transport, agentBuf net.Buffers) bool {

	flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
	// We do route lookup once for L4 flows
	if *dest == nil {
		*bundle, *dest = l4Lookup(s, MyInfo, ctx, onboard, flow)
		s.Debugf("Agent L4 Lookup", flow, *bundle, *dest)
		// L4 routing failures need to terminate the flow
		if *dest == nil {
			s.Debugf("Agent flow dest fail:", flow)
			return false
		}
		*dest = (*dest).NewStream(nil)
		if *dest == nil {
			s.Debugf("Agent flow stream fail:", flow)
			return false
		}
	}
	usrattr, attrok := aaa.GetUsrAttr(atype(onboard), onboard.Userid, s)
	if attrok {
		flow.Usrattr = usrattr
	}
	// The AAA access check is done per packet today, we can save some performance if we
	// do it once per flow like route lookup, but then we lose the ability to firewall the
	// flow after its established
	if *bundle != nil {
		if !aaa.AccessOk(atype(*bundle), (*bundle).Userid, flow.Usrattr, s) {
			s.Debugf("agent access failed ", *bundle, flow.DestAgent)
			return false
		}
		// Going to another agent/connector, we dont need the attributes anymore
		flow.Usrattr = ""
	}
	err := (*dest).Write(hdr, agentBuf)
	if err != nil {
		s.Debugf("Agent flow write fail:", err, flow)
		return false
	}

	return true
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
func handleAgent(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport) {
	var first bool = true
	var l4dest common.Transport
	var l4bundle *nxthdr.NxtOnboard

	onboard := sessionGet(Suuid)
	if onboard != nil {
		// this means we are not the first stream in this session
		first = false
	}
	s.Debugf("New agent stream", Suuid, onboard)

	for {
		hdr, agentBuf, err := tunnel.Read()
		if err != nil {
			tunnel.Close()
			if l4dest != nil {
				s.Debugf("Agent read error, dest closed", err)
				l4dest.Close()
			}
			if first {
				// If the very first stream on which we onboarded goes away, then we assume the
				// session goes away
				sessionDel(Suuid)
				agentDel(s, MyInfo, onboard, tunnel)
			}
			s.Debugf("Agent read error", err)
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
				agentAdd(s, MyInfo, onboard, tunnel)
				s.Debugf("Onboarded agent", onboard)
			}

		case *nxthdr.NxtHdr_Flow:
			flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
			if flow.Type != nxthdr.NxtFlow_L3 {
				if first {
					panic("We dont expect L4 flows on the first stream")
				}
				if !l4Fwd(s, MyInfo, ctx, onboard, hdr, &l4bundle, &l4dest, agentBuf) {
					tunnel.Close()
					if l4dest != nil {
						l4dest.Close()
					}
					return
				}
			} else {
				if !first {
					panic("We expect L3 flows only on the first stream")
				}
				// l3 fwd packet drops are a NO-OP
				l3Fwd(s, MyInfo, ctx, onboard, hdr, agentBuf)
			}
		}
	}
}

func l3Local(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, hdr *nxthdr.NxtHdr, podBuf net.Buffers) {

	flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
	// Route lookup per packet for l3 flows
	o, dest := localLookup(s, flow)
	if dest != nil {
		if !aaa.AccessOk(atype(o), o.Userid, flow.Usrattr, s) {
			s.Debugf("Interpod: l3 aaa access failed ", flow.DestAgent)
			return
		}
		// Going to an agent/connector, we dont need the attributes anymore
		flow.Usrattr = ""
		err := dest.Write(hdr, podBuf)
		if err != nil {
			s.Debugf("Interpod l3 dest write failed", flow, err)
			return
		}
	}
}

func l4Local(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, onboard **nxthdr.NxtOnboard,
	hdr *nxthdr.NxtHdr, dest *common.Transport, podBuf net.Buffers) bool {

	flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
	// Route lookup once for L4 flows
	if *dest == nil {
		*onboard, *dest = localLookup(s, flow)
		s.Debugf("Interpod L4 Lookup", flow, *onboard, *dest)
		if *dest == nil {
			s.Debugf("Interpod: cant get dest tunnel for ", flow.DestAgent)
			return false
		}
		*dest = (*dest).NewStream(nil)
		if *dest == nil {
			s.Debugf("Interpod: cant get dest tunnel for ", flow.DestAgent)
			return false
		}
	}
	// The AAA access check is done per packet today, we can save some performance if we
	// do it once per flow like route lookup, but then we lose the ability to firewall the
	// flow after its established
	if !aaa.AccessOk(atype(*onboard), (*onboard).Userid, flow.Usrattr, s) {
		s.Debugf("Interpod:  aaa access failed ", flow.DestAgent)
		return false
	}
	// Going to an agent/connector, we dont need the attributes anymore
	flow.Usrattr = ""
	err := (*dest).Write(hdr, podBuf)
	if err != nil {
		s.Debugf("Interpod: l4 dest write failed ", flow.DestAgent, err)
		return false
	}

	return true
}

// Handle data that arrives from other pods, the other pod can be on the local cluster or it might be on
// a remote cluster. Basically the other pod did a route lookup and found that this pod (where this API runs)
// is hosting the agent/connector of its choice, so as far as this API is concerned, its destination stream
// is always local
func handleInterPod(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context, Suuid uuid.UUID, tunnel common.Transport) {
	var l4dest common.Transport
	var onboard *nxthdr.NxtOnboard

	s.Debugf("New interpod stream", Suuid)

	for {
		hdr, podBuf, err := tunnel.Read()
		if err != nil {
			tunnel.Close()
			if l4dest != nil {
				s.Debugf("InterPod Error, dest closed", err)
				l4dest.Close()
			}
			s.Debugf("InterPod Error", err)
			return
		}
		switch hdr.Hdr.(type) {
		case *nxthdr.NxtHdr_Flow:
			flow := hdr.Hdr.(*nxthdr.NxtHdr_Flow).Flow
			if flow.Type != nxthdr.NxtFlow_L3 {
				if !l4Local(s, MyInfo, ctx, &onboard, hdr, &l4dest, podBuf) {
					tunnel.Close()
					if l4dest != nil {
						l4dest.Close()
					}
					return
				}
			} else {
				// l3 packet drops are a NO-OP
				l3Local(s, MyInfo, ctx, hdr, podBuf)
			}
		}
	}
}

func RouterInit(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	sessions = make(map[uuid.UUID]nxthdr.NxtOnboard)
	agents = make(map[string]common.Transport)
	pods = make(map[string]*podInfo)
	localRoutes = make(map[string]*nxthdr.NxtOnboard)
	unusedChan = make(chan common.NxtStream)
	go outsideListener(s, MyInfo, ctx, "websocket")
	go insideListener(s, MyInfo, ctx, "http2")
}

// Open a Websocket server side socket and listen for incoming connections
// from agents, and for each agent connection, spawn a goroutine to handle that
func outsideListenerWebsocket(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	var pvtKey, pubKey []byte
	server := websock.NewListener(ctx, pvtKey, pubKey, MyInfo.Oport)
	tchan := make(chan common.NxtStream)
	go server.Listen(tchan)
	for {
		select {
		case client := <-tchan:
			go handleAgent(s, MyInfo, ctx, client.Parent, client.Stream)
		}
	}
}

// Open a Quic socket to listen for incoming connections from other pods in the
// same cluster or other pods from outside this cluster
func insideListenerHttp2(s *zap.SugaredLogger, MyInfo *shared.Params, ctx context.Context) {
	var pvtKey []byte
	var pubKey []byte
	server := nhttp2.NewListener(ctx, pvtKey, pubKey, MyInfo.Iport, "x-nextensio-session")
	tchan := make(chan common.NxtStream)
	go server.Listen(tchan)
	for {
		select {
		case client := <-tchan:
			go handleInterPod(s, MyInfo, ctx, client.Parent, client.Stream)
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
