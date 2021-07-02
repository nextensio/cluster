# Architecture/Terminologies

Nextensio is fundamentally a "flow forwarding" system where the Agent (running on client phone/laptop)
identifies user 'flows' and then sends it via a series of overlay hops to the final destination server

[User]Agent--[Overlay]--Apod---[Overlay]--Cpod----[Overlay]--Connector---Internet/PrivateNetwork[Dest Server]

The Apod and Cpod are part of what we call the nextensio 'cluster' or 'gateway' running the 'minion' 
software as kubernetes pods. the Apod and Cpod can be the exact same pod, or two different pods in
the same cluster or two different pods in two different clusters. The architecture or the code design
should not differentiate or have special cases for these, all three should behave just the same from
a design perspective

## Agent/Connector 

There are three modes for agents/connectors in nextensio.

### Proxy mode

This is the mode where the applications (like browsers) that support an http proxy, will send a "CONNECT"
request to the agent and the agent will send the CONNECT all the way to the connector and the connector
will open an http(s) socket to the destination and foward the data back to the agent. 

### L4 mode

L4 mode is where the agent intercepts application's tcp/udp traffic, TERMINATES the tcp/udp session on 
the agent, sends the payload to the connector, and connector opens a new tcp/udp session and stitches
them together.

### L3 mode

L3 mode is where the agent intercepts appliation's raw IP packets and sends it to connector. And connector
then Source-NATs those packets to connector's eth0 ip address and sends it out. And of course the connector
maintains NAT mappings such that when the return packet comes in, it knows which flow it maps to from the
NAT entries that it saved, and thus sends the flow back to the originating agent

## Sessions and Streams (ie flows)

## Proxy/L4 mode

As described before, the proxy and l4 mode agent data we carry are actually agent "flows" that we 
carry across the cluster. We have no choice but to maintain some context for each of thes "flows"
end to end across all nextensio hops - because we might want to do QoS stuff to ensure that a slow
flow does not affect a fast flow etc.. (head of line blocking). If we did not care about that aspect,
we could have ignored all the flow context and just downgraded to a pure packet router model. 
So, how do we maintain the flow identity when we send the flow from one hop to another ? There are
two options

1. Open individual tcp sessions for each flow
2. Have one tcp session and "multiplex" the flow information over it. And the mechanism/encoding
   that the session uses to differentiate flows over it is what we call "streams". That is, flows
   and streams are basically interchangeably used

Option 1 ends up with too many tcp sessions, so we choose to go with option 2. Also note that
it does not have to be a "tcp" session, it can be a udp session also as long as the underlying
udp transport some how gives us reliability (QUIC protocol is an example)

## L3 mode

In L3 mode, we are just carrying application's raw ip packets, this is a degraded mode of operation
and we dont strive to provide any QoS guarantees here. So all these L3 packets are bunched together
into one single stream regardless of how many different applications are originating those packets.

NOTE: It is still possible to provide qos treatment in L3 mode using traditional routing world
techniques like queues and qos schedulers and wred drops etc.., but thats not the value add of 
nextensio solution and we dont want to go that route

The other problem with L3 mode is that if the underlying session used to carry the raw IP packets
is a tcp session, then we will run into the problem of "tcp meltdown" if we carry tcp IP packets
over a TCP session. Theres plenty of literature about tcp meltdown on internet. So this limits us
to the fact that if we want L3 mode, then the session has to be UDP based - it will still work if
its tcp based, but can run into the meltdown performance issue. But if we use UDP as the session,
then we run into other limitations as described in sections below

## Session protocol and Nextensio cluster

The key design principle of nextensio is that we want to leverage the open source web frameworks like 
kubernetes and service meshes like istio to do a lot of cool stuff like traffic steering and stats etc..
inside the nextensio cluster. So the overlay should be a protocol that should be recognizable by these 
open source frameworks - and today the only choice is http/http2, hopefully http3 sometime in future. 
So the sessions and streams we mentioned in the previous sections have to end up having some http/http2
framing so that we can utilize kubernetes/istio to our benefit. So anything we chose as the protocol
in any of the legs (agent--cluster, apod--cpod, cpod--connector) has to have some http framing AND it
also needs to have the ability to multiplex streams on top of it. Here is what we plan as of date

1. From agent/connector to cluster, we can use websocket because websocket "starts off" with http 
   framing. Note the "starts off" mentioned above, we will elaborate on that in a bit. But websocket 
   is just the "session", it does not have any "stream/multiplexing" capabilities on top of it. So
   either we write a small multiplexing layer on top of it (which is the case as of date) OR we use
   a library like rsocket which does exactly that, rsocket can have streams on top of websocket. As
   of writing this, the rsocket golang lib is very raw and not stable, so thats a future consideration

   Also note that for the agent/connector in L3 mode, for reasons of avoiding tcp meltdown as discussed
   earlier, we need UDP transport - but UDP is not recognized by istio and that makes it difficult. So
   most likely if we do L3 mode, it will still be over tcp (websocket) and we will just have to live
   with the possibility of a degraded meltdown scenario

2. Between apod--cpod (same cluster or different clusters), we will use HTTP2. HTTP2 inherently 
   supports streams. So one can ask why not use http2 in 1) above, for agent/connector--cluster ?
   The reason is that http2 streams are for all practical purposes "uni directional" - ie a client
   can initiate a new stream to a server, but not vice versa. This is ok for apod---cpod talk because
   apod and cpod can both run as http2 servers and hence we can establish two seperate streams 
   apod--->cpod and cpod--->apod. But that wont work in the case of agent/connector. Agent/connector
   cannot run an http2 server beause they can only dial out, so for agent/connector we need some
   mechanism to create streams from server to client - and hence why we cant use http2.
  
   One may also ask - so in that case can we just use websocket+streaming for apod--cpod also ? The
   answer is "not really" - because for websocket, the only thing that is visible to kubernetes/istio
   is the "session" http framing, our custom stream protocol (or rsocket) on top of it will not be
   visible. This is just fine for agent/connector because most of the times at the point where the
   agent/connector enters the cluster, we just want to apply policies based broadly on who the 
   agent/connector is and we dont care about the flows from the agent/connector at that point. But
   once inside the cluster, we want to know the flows from the agent/connectors and hence we need
   some framing which istio/kubernetes understands, and thats http2

Now coming to the mention above of "websocket starts off with http framing": So the only time istio
can interpret anything in a websocket session is at the very beginning of the session establishment
where websocket talks http framing - after the session is established, it switches to custom framing
and binary data. So then how does istio make any sense out of it ? So the point to clarify here is
that istio/kubernetes comes into picture only during the initial handshake of session establishment
for websocket and initial handshake of stream establishment for http2. After the initial handshake
is done and istio does its job of redirecting the session to whichever pod we configured it to, then
it doesnt have to look into every frame post that point. Now is that ok for us ? Yes its ok, we want
to be able to influence a traffic "flow" using istio, at the point that its getting established, 
after that we dont need istio to do stuff for us, we do per packet (per data frame) magic using
other policies like OPA. 

In other words, when a websocket/http2 session/stream is getting established, we need to throw in
http headers that are required to influence istio/kubernetes, after that point we dont need to
worry about http headers and we can send our own framing inside that session/stream. But remember
that as we mentioned few paragraphs before, websocket has the limitation that istio will never be
able to understand it at a "stream" level like it understands http2 - so the amount of control we
can have over an agent/connector at the entry point to a cluster is purely at a broad agent/connector
level and not at a flow level

### Nextensio framing format

So based on our discussion above, this is how the framing will look like below

#### Websocket

1. First frame with upgrade request {HTTP headers where we can insert our own headers}

2. Next frames: {Websocket binary frame {Nextensio protobuf header}{Nextensio payload}}

Note that websocket binary framing encodes details of the websocket payload length (in this case
the protobuf header + nextensio payload) and all websocket libraries will give one full frame
on a read, so its convenient to use those libs since we dont have to deal with partial tcp data

The other thing to note here is that inside the nextensio protobuf header, we will further have
information identifying the stream (flow) etc.. If we were to use rsocket, then rsocket will
have one more level of framing after websocket framing and before nextensio protobuf, to 
include this same information about streams

#### HTTP2

1. HTTP HEADER frame: {HTTP headers where we can insert our own headers}

2. Next frames are HTTP BODY frames: {Nextensio protobuf header}{Nextensio payload}

Note that because we are just sending a never-ending saga of http body, and http2 can end up
sending multiple BODY frames for one set of nextensio protobuf + payload, we will need to deal
with partial data and waiting till the whole data is read in etc..

Note that we choose protobuf codec here over using a text based http codec because protobuf
saves a ton of space in encoding, and it has useful features like skipping zero fields etc..
The other thing to note is that in the framing mechanism mentioned above, every nextensio 
payload will have the nextensio protobuf headers - we should be able to further optimize on
that by sending only the delta between each payloads etc..

## Programming models around sessions/streams

From all the discussions above, we can see that the exact mechanism we choose for each leg
will always have new options - tomorrow rsocket for agent/connector and http3 for apod--cpod
for example. So its extremely important and required that we abstract away all the details 
of the underlying mechanism and expose to the agent/connector/minion code just the details of
sessions and streams. So here is what the agent/connector/minion needs to know

1. A session needs to be dialled from a client to a server, with all the TLS handshake parameters
   and stuff configured. The session is not really what carries the flows, the session is just
   the large pipe inside which there will be smaller pipes (streams) that carry flows

2. A stream can be created over a session - preferably from both directions - ie from client
   to server and from server to client. But as discussed before there are sessions like http2
   which can only create streams from client to server, so we need to be aware of that. Also 
   there should be no limit on the number of streams that can be created on a session, or if
   there is a limit the abstraction should hide it somehow - for example http2 hides all the
   details of sessions from the library user - behind the scene http2 might dial more than one
   sessoin to split streams over sessions, but library user doesnt have to worry about it

3. A stream has to have the ability to be flow controlled - to prevent one fast stream from hogging
   others and vice versa of one slow stream from slowing others (head of line blocking). We 
   are talking about client/server end points of the stream seperated over internet, so the 
   flow control mechanism obviously has to be done by exchanging some kind of window update or
   backpressure frames etc.. and we know that will not be a perfect mechanism (as compared to
   the flow control mechanism in a hardware router with hardware backpressure for example)

4. Streams need to have the ability to be closed from either end - ie a stream created by
   a client should be able to be closed by a server and vice versa

### Streams and event driven programming / goroutines

If we think of "what really is a stream", a stream after removing all its bells and whistles 
is two queues - an rx queue and a tx queue. So if we have a stream per flow, we have queues
per flow. And obviously someone needs to read from the rx queue of one stream and process it
(write to the tx queue of another), and read from tx queue of the stream and transmit the data 
on the session. 

The standard way of doing this is to have a set of threads (thread pool) and each thread will 
be assigned a number of queues which it will monitor in an event driven fashion (somehow each
queue being non-empty should trigger an event to the thread). We can hand craft an infrastructure
for that ourselves, but if we look at what a goroutine is, that is EXACTLY what we described
above - a goroutine is a function that gets called from a set of threads (pool) in response
to an event! A goroutine is NOT a thread by itself, its a "green thread" - ie if we look under
the hood of the go language, thats exactly what the language does - there will be a thread 
pool and the goroutines are all units-of-work which are done based on an event trigger, and
in golang the events are usuall channel messages.

Now, the question is whether a goroutine is a better way of doing an event driven model or
something hand crafted will perform better Hard to say, goroutines are being worked upon the
entire community being performance tweaked under a microscope - stil because its a generic
framework, one can imagine a special purpose hand crafted event driven logic will perform
better. But for the moment, the amazing speed at which one can code with goroutines, the
convenience it provides, and the ease at which one can understand code written like that, 
all makes us go with the goroutine model. We are a startup and speed at which we can move
matters as much as performance, so at some point when we are succesful, we can evaluate our
own event routines if we think that performs better

The one thing that might make us reconsider moving away from goroutines to a custom
scheduling mechanism might be memory usage. A goroutine takes up minimum 2Kb stack
size as of today (go version 1.5). And we have a goroutine per Rx and Tx today, plus
maybe an additional one or two goroutines depending on the stream library we use in
common/transport (which potentially can be optimized). So there is like two to four 
goroutines per flow - which is 8Kb memory. And each flow will have data packets queued
up, we define common.MAXBUF as of today as 2Kb. So assuming each flow has minimal data
queued up ie 2Kb, that means the total minimum memory per flow is 8Kb + 2kB = 10Kb
which is quite a lot ! So at some point if memory starts killing us when we scale, we
"may" have to evaluate moving away from goroutines and basically writing our own 
unix select/poll loops using a thread pool etc.. - but then thats no more idiomatic
go and then we have to ask ourselves whether we even want to stick to Go or move to
some other faster language like Rust ?

But again, we are talking about a future thats at least four years away - when we
start getting hammered by scale issues. And as mentioned before, its always a balance
between how much time we spend on things that take up a lot of time and potentially
slow us down a lot and prevent us even from reaching that point we want to reach 4 years
down the lane Vs how much slack we knowingly take right now in favour of simpler 
easier code which allows faster feature development so that we become successful 
and have a chance to face the problems we are worried of. If anyone has coded in 
Rust for example, they wll know that its like ten times harder to code and we probably
will never get started off the ground to begin with!


#### goroutine programming template

Another requirement for being able to move faster as a team / a startup is the need for 
standard well behaved cookie cutter templates people can cut+paste instead of having to think
of a solution each time. The below is a cookie cutter model for stream programming using
goroutines. This is how we work using sessions and streams

1. The very first time, Dial a session using the standard golang Dialer APIs provided by the 
   stream abstraction. The dialler is also provided with a channel on which we get notified
   if the other end of the session initiates a stream, so we need to monitor that channel

   On the very first stream on a session from an agent/connector, we expect to get onboarding
   info from the agent/connector. We DO NOT expect agent/connector to send us any data over
   the first stream before thats done and we do not exect agent/connector to create new streams
   before onboarding over first stream is done. This first stream is also used to carry l3 
   payload, read below for more info

2. If a session exists and we want a new flow/stream over the session, we call the NewStream()
   API, we get a new stream and we spawn a goroutine to read from the stream. Instead if 
   the other end initiates a stream to us, we get notified on the channel above and we get
   the stream on the channel and spawn a goroutine to read from the stream. The goroutine
   will look as below

   ```
   // Stream processing for layer4/proxy tcp payload. We have one stream per tcp flow
   // An l4/proxy data CANNOT be dropped, drop should termintae the flow
   func streamReader(rxStream) {
       var txStream common.Transport
       for {
           nxtHdr, data, error := rxStream.Read()
           if error != nil {
               // closing rx and tx streams will result in a cascade of closures all
               // the way to the agent connector which will finally terminate te flow
               rxStream.Close()
               txStream.Close()
               return
           }

           // Lookup the Tx stream, create if one doesnt exist to the destination
           // Destination can be in the same pod, different pod in same cluster or
           // a different cluster altogether, it does not matter, consul tells us
           // the final ip address/details to reach the agent. Note that once we
           // lookup a txStream, it remains the same for the life of this flow
           txStream = lookup_or_create(nxtHdr.DestAgent)
           // If route lookup failed or some error happened, we cant just drop the
           // frame, we need to terminate the flow
           if txStream == nil {
               rxStream.Close()
               return
           }
           error := txStream.write(nxtHdr, data)
           if error != nil {
               rxStream.Close()
               txStream.Close()
               return
           }
       }
   }

   // Stream processing for l3 packets, agents/connectors just send all l3 packets on one
   // stream, the very first stream. And we send l3 frames from apod<-->cpod on just one
   // stream. L3 packet drops are benign, the agent/connector's tcp stack will retry, so 
   // we dont have to close any streams, plus we should NOT close the stream becuase its
   // one stream for everybody on l3!
   func streamReader(rxStream) {
       for {
           nxtHdr, data, error := rxStream.Read()
           if error != nil {
               // Well if the stream has a read error, then theres something wrong with
               // the underlying connectivity, no choice but to get out of here
               rxStream.Close()
               return
           }

           // Lookup the Tx stream, create if one doesnt exist to the destination
           // Destination can be in the same pod, different pod in same cluster or
           // a different cluster altogether, it does not matter, consul tells us
           // the final ip address/details to reach the agent. Note that this 
           // txStream keeps changing per packet
           txStream = lookup_or_create(nxtHdr.DestAgent)
           // If route lookup failed or some error happened, just ignore
           if txStream == nil {
               continue
           }
           error := txStream.write(nxtHdr, data)
           if error != nil {
               continue
           }
       }
   }

   ```

3. If there any kind of errors, just close the streams in the goroutine and return.
   Read comments above regarding l3 streams, we dont have to close streams in 
   case of l3 pkts

4. We use two streams for the same flow - one in Rx direction and another in Tx,
   read the section on "Seperate Streams for each direction" below for more details
   and info on why we do that

### Streams and QoS, buffering/memory

If we look at the stream template above, we can see that the general model is that
a goroutine reads from a stream and writes to another stream. The write is a blocking
write, so if we are reading from a fast stream and writing to a slow stream, we will
end up blocking the goroutine. This will mean that the rx queue of the read stream
will keep growing and the underlying stream library will have its own mechanisms to
have watermarks for rx queue depth and if it exceeds some limit, the underlying 
library will initiate a flow control / window update to the sender - all this happens
automatically inside the stream abstraction without the knowledge of the coder who
follows the template above. 

But the after effect of the above mechanism is that the rx queue depth determines
the amount of data that gets queued up. So inside a minion, we can have buffers 
queued up per flow based on how fast we can process them and also based on how fast
the underlying tcp sessions are. So now we are entering the territory of standard 
QoS problems namely

1. How much should be the queue depth - if we have a ton of slow flows in the system
   it can eat up a ton of memory. So we might want to reduce queue depth. But if we
   reduce queue depth too much, that means we might trigger window updates too 
   frequently and that will penalize a fast flow if it bursts even a little. There
   is no one good answer here, this will be a tweaking effort based on what we want
   from the system - this also will depend on how the underlying stream mechanism
   works (like http2/rsocket) and whether it will allow us to tweak the queue
   depths or if we will need to tweak the http2/rsocket library itself to achieve that

2. How much should be a packet buffer size - the easiest approach we can take is 
   to say that a nextensio frame is at most 64K in size, so we always allocate 64K
   size buffers. But then the problem is that if there are many slow flows sending 
   tiny amounts of data, there will be a lot of 64K buffers sitting around with 
   say just 1K of data in it. This is also a VERY STANDARD problem in networking
   with a standard solution of having data chained as multiple buffers.

   So all our stream abstractions provide the ability to chain data using the
   golang net.Buffers interface. Golang makes it easy to hide the details of chaining
   by providing reader/writer io interfaces. The size of the buffer is something 
   we again have to tweak in future - we can always degrade to the one big buffer 
   model by having a chain of ONE buffer with 64K size for example, or we can have
   multiple buffers of 2K in size.

   As of today the buffer size is kept as 2K. One limitation we have placed to make
   the stream library code simple is to mandate that the nextensio headers should
   all fit in one single buffer - practically speaking that should not be an issue,
   but if we ever have some header thats humongous (like the user attributes), then
   we can either increase the size of the buffer or change the stream library to be
   aware of fragmented headers.

   So one can ask - what is the disadvantage of having multiple buffers ? That really
   depends on the individual stream implementation, I would recommend looking at the
   comments in the websocket code in the gitlab common repository - the websocket 
   gorilla library we use will end up doing more memcopies if we use chanined buffers
   as opposed to one memcopy with a single huge buffer. So its a compromise between
   whether you have a lot of memory to throw around for better performance Vs if you
   want to conserve memory with some hit to performance


### Seperate Streams for each direction (Rx/Tx)

When an agent initiates a flow, it will create a stream to the cluster, and its 
perfectly valid to expect that the return for the flow comes on the same stream.
Similarly the cluster initiates a flow (from agent) to the connector on a new 
stream to the connector and its valid to expect the connector to send back the 
response on the same stream.

The above model of same stream for rx & tx works very well in a request/response
model where the agent is always intiating the request and connector is responding
to it. But our architecture is by no means limited to a request/response model and
we envision one day where we can have agents and connectors initiating to each other,
or rather two agents initiating to each other / talking to each other. And when that
happens, the SAME FLOW can be created by a packet from the agent to the cluster 
and AT THE SAME TIME by a packet from the cluster to the agent - a very classic example
is realtime communicaton protocols where two people talking to each other will start
dialling each other parallely and both can end up being the exact same flow in an 
enterprise environment with no NAT etc. 

And at that time if we try to go with the mandate that we need ONE stream for the 
flow, that will hit us with a ton of synchronization issues and timing bugs - this
comes from a past experience where we tried to mandate this "one stream" mantra and
kept fixing bugs and plugging holes for the rest of the life of the product. So the
earlier we start accepting the fact that there are two seperate streams for a flow
and the earlier we adopt our programming template to that thinking, it will ensure
that the code remains simple / race free / timing free and easy to understand -
yes it will have the tax of an additional stream, but I can vouch heavily from my
experience that the tax is worth the man hours wasted debugging race conditions

## Things to know before building a docker image

The minion code uses the stream library which is in gitlab in the common repository,
it is common because agents and connectors both share that code and we want to
ensure that anyone making changes in that library thinks about the entire system
and not just one component.

The docker build process does a git clone of the minion repository inside the 
docker container and tries to download all golang packages (go get) from inside
the docker container. So obviously to be able to pull the private gitlab common
repository, the docker container needs a read-only key to the common repository,
the key is copied to the container and removed once the gitlab common repo is 
downloaded inside the container. And on top of that go get is not very friendly
when it comes to getting code from private repos, it needs a few flags and tweaks
all described in the README.md file in the common repo

https://gitlab.com/nextensio/common/-/blob/master/README.md

## Pod replicas Load balancing, health checks, outlier detection

The primary architecture goal here is to let kubernetes/istio figure out how 
connections are allocated pods - there were alternate proposals to let the 
controller control pod allocations. But we dont want to get into that business
becase if we do, then we need to be aware of various k8s parameters like load,
memory, failure rate etc.. ourselves, and then we are just replicating the stuff
that k8s/istio has already built-in. And our goal is to work like a regular web
application in a k8s/istio environment, so lets just align as much as possible to
how people generally do stuff in kubernetes/istio

### Stateful sets

The agent pods (apods) are configured as stateful sets with replicas. The agent pods are 
stateful sets because the apod to connector pod (cpods) session is unidirectional (http2)
and the response from cpod is a new session, and the response has to reach the same
apod as the one that originated the request.

The cpods are also configured as stateful sets, althought that is not necessary. Because
it is configured as stateful sets, we are using a headless service by which consul monitors
the health of each cpod replica - but we should be able to find some other way for consul
to monitor replicas, so if need be, cpods can move away from being stateful sets

When agents connect to the cluster, istio loadbalances the agents to one of the apods, 
and as mentioned before, the cpod to apod connection is targeted to a *specific* pod and
hence there is no loadbalancing there.

### Cpod replica allocation

When connector connects to the cluster, istio loadbalances that too, although there is a subtle
difference compared to the apod case. For a customer who has say a thousand users, we might 
provision say 50 apods. And the users might keep coming and going based on when they connect
their device or how they roam around etc.., and so we expect that to be very 'dynamic' - and
istio can loadbalance thousand users among 50 pods pretty well.

But in case of connectors, its a more static / less dynamic situation. Each connector has its
own cpod/cpod-replicas. How a customer will provision a cpod for a connector is based on the
customers knowledge of how much bandwidth they need for that connector. So for example a streaming
video connector service might need more bandwidth and hence more cpod replicas than another
connector used for point-of-sales billing systems. So the customer might say that "I need totally
100Mbps bandwidth for this connector" and then nextensio will say "based on our performance, we
will need 10 replicas for this connector, because one connector can do only 10Mbps (for example)"

And so on the cluster side we will create 10 replicas, and at the customers data center, they will
spawn ten connectors and try to connect to our cluster. Now, kubernetes is not all that great when
it comes to loadbalancing a small number of sessions - it can end up such that customers ten 
connectors land up on say five replicas, with each replica having two connectors. And that defeats
the purpose of us having allocated 10 replicas if 5 are unused. So in this case, we are not really
interested in "load balancing", we just need to allocate one connector to each replica. 

### Outlier detection

We do that in pretty simple terms by basicaly not allowing any connections on port 443 (onto which
connectors connect) after a replica has one succesful connection. So next time a connector attempts
to connect and istio directs it to this replica, it will reject the connection. But this brings in
a new problem which is that istio/envoy will keep on trying new connections to this replica which
will not accept any more - its not too bad, because istio will move onto try other pods since we 
are just configuring it to do round robin loadbalancing. But every now and then the round robin will
land up on this replica and we will again reject the connection - its good if we can avoid that, and
yes we can ! So for that we use a simple outlier detection in envoy - we tell envoy that "if you see
a rejection, then take it out of the loadbalancing pool, add this to the pool again after xyz time 
and give it a chance to see if it can connect at that point, if not take it out again" - and this
is what envoy calls "outlier detection", and it has simple tunable parameters, some are exposed 
via istio configs and some unfortunately has to be plugged in as envoy filter configs

### Health check

The above talks about connections from connectors to the cpod. Now lets think about connections
from apods to cpod (on port 80). What we do is we start off with port 80 being closed till we
have at least one connector connect to the cpod - because we cant accept connections from apod
unless we have a connector on this cpod. And when we have a connector connecting to the cpod,
we open up port 80, and when connector goes away, we close port 80 etc..

We can in theory use the same outlier detection for port 80 also and it will work just fine,
except there is one issue - the outlier detection will bring the replica back into the loadbalancer
pool every now and then, and that means the apod to cpod session will every now and then
fail and then work fine again for a while (because envoy takes it out of loadbalancing) and 
then have one failure again etc.. - and thats not acceptable.

So for port 80 (apod to cpod port), we resort to "active health check" - outlier detection is
a "passive health check". So we tell envoy to "actively" check if port 80 is open using a 
keepalive kind of check (rather than waiting for customer connection to fail) and if the 
active check fails, then take the replica out of loadbalancing. Unfortunately this needs an
envoy filter config, its not natively supported in istio.

### Consul health check

Lets say we registered a service with consul from cpod. And now we go and remove the cpods - 
ie kubectl delete -f cpod.yaml. Unless we do something specific, the service will stay forever
in consul. So we have to register a health check with consul. For this reason we create a 
"headless service" for the cpod stateful set - the headless service basically exposes a DNS
domain name for each replica of the stateful set. And on each replicas dns name, we use 
port 8080 as a consul health check port. So if the pods are removed, then the health check will
eventually fail and consul will remove the entries from its catalog


