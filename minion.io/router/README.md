# Design brief

We briefly talk about the principles guiding the design of the router here. The routing is basically a
"cross connect" of two sessions - called "left" and "right" in the code. The "left" refers to a connection
from the agent/connector to the minion, and the "right" refers to a connection to another pod in the same
cluster, or a completely different cluster. The picture below hopefully clarifies it, do not confuse left
and right with the direction of flow of packet, its just based on "who" is connected to the other end of
that session (agent/connector(L) or pod/cluster(R))

```
Agent---(L)Minion-Pod1(R)---(R)Minion-Pod2(L)----Connector

Agent---(L)Minion-Pod1(R)---Remote cluster
```

The one thing to keep in mind is that we are from a high level nothing but a tcp proxy - we terminate
tcp sessions from agents and then proxy it via multiple tcp sessions all the way to the connector and then
to the final destination server. Of course there are cases where we also do raw IP packet transport,
but those will be the minority of the use cases (and simpler use case), the majority (and complex) will
be the proxy use case. So if we think about how proxies will be designed, we can get an understanding 
of our design too

## Proxies and Connections

This topic of connections comes into play when thinking about two problems

1. How to backpressure tcp sessions properly even if they are going via proxy
2. How to handle the case of a failure to forward a packet in a proxy

Proxy is end of the day a piece of software running on a server somewhere - so it has memory limitations.
So no proxy wants to end up eating a ton of memory on behalf of its clients, especially if the proxy is 
built to scale to tens of thousands of clients/sessions. And not just from the point of view of proxy, 
even from point of view of the client, no client wants a proxy to buffer data - because buffering TCP
data makes TCP performance far worse than actually dropping packets - there is a TON of literature on
internet about this, google for "buffer bloat". So the proxy wants to ensure that it backpressures its
tcp connections appropriately thus trying to avoid buffering data.

Now a more serious problem is dropping data - a proxy cannot afford to drop data for any reason. Because
the data the proxy tries to forward is already TCP-ACKED with the client. So the client thinks that the
TCP data it sent has already been ACKed by the server, but its really only been ACKed by the proxy. So
traditionally, if the proxy has to drop a packet for whatever reason, it just closes both its
left side and right side session and so eventually the client/server session is closed in ints entirety

###  Multiplexed Vs 1:1

#### 1:1

Consider a traditional web proxy like below

```
Client--conn1--Proxy1--conn2--Proxy2--conn3--Server
```

In this case client thinks its talking to server directtly, but is actually going via two proxies, 
so "conn1" and "conn2" and "conn3" are independent TCP connections which are "cross connected", that is,
what comes on one side is simply thrown into the other side.

The most easy thing to understand is the model where a proxy just opens a new tcp outgoing connection for 
every incoming connection - which means that in the above diagram, conn1, conn2, conn3 are all uniquely
representing a SINGLE connection between the client and server. If the client and server opens another 
connection, or a different  client opens a connection to the server via the same proxies, then we will
have a complete different set of connections connX, connY and connZ

So in this case, lets consider how the points 1 and 2 (backpressure and loss) are handled

##### Backpressure and Single threaded Vs two threaded

Lets say the Client download speed is 1Mbps but the Server to proxies have fantastic speed of say 1Gbps,
this is very common because the last mile is always the slowest. Now lets say client is doing speedtest
and the server is speedtest.net. The server to proxy2 conn3 session will initially download data at 1Gbps
and it will be sent at the same rate from Proxy2 to Proxy1 and Proxy1 has no lack of cpu compute poweer,
so it will pull out data at 1Gbps and try to send it via conn1. But conn1 is a 1Mbps connection, so at 
some point the Proxy1's tcp stack will stop getting ACKs from the client side because the connection is 
slow, so the proxy1 conn2 reader thread will stall trying to write the data to conn1

```
Proxy1_Reader() {
    for {
        data = read(conn2)
        write(conn1, data) <======== This will block if the agent is slow
    }
}
```

Now lets say the proxy had an alternate design as below with seperate reader and writer threads

```
Queue = make_Queue(1Mb)

Proxy1_Reader() {
    for {
        data = read(conn2)
        Queue.enqueue(data)
    }
}

Proxy1_Writer() {
    for {
        data = Queue.dequeue()
        write(conn1, data)
    }
}
```

In the two threads + queue model, what will happen is that the queue acts as an additional buffer on top
of the kernel tcp buffers. So the speedtest.net server will pump data for maybe a few milli seconds more
at 1Gbps rate before eventually it gets backpressured. It does get backpressured eventually because the
queue of 1Mb size will fill up without any doubts if speedtest pumps data at 1gbps. And at that point, 
remember we cant drop packets because we are a proxy, so when the queue is filled up, the Reader thread
will be blocked when it tries to enqueue to the queue - so the end result is finally the same - in both
models (1 thread and 2 thread), finally we block, except that in the two thread model we block AND we eat
up a lot of memory, and that extra memory usually does not buy any additional performance gains, the 
buffer bloat research on internet has proved that it infacts degrades performance

**NOTE: A DESIGN CHOICE CONCLUSION: For the above mentioned reason, in our code we have the single thread
reader+writer model**

##### Packet drops

Now if for whatever reason we want to drop a packet after reading it from a connection, we have to ensure
we close the left and right side connections - so proxy1 closes conn1 and conn2 if it reads something from
conn1 and ended up dropping it - this ensures that the client sees a connection closure, this also ensures
that proxy2 sees conn2 closure and hence closes conn3 also, and this ensures the server connection is closed
too

#### Multiplexed

```
Client1--conn1--Proxy1--conn2--Proxy2--conn3--Server1
                |                   |
Client2--conn4--+                   +--conn5--Server2
```

In the above diagram, we can see that the conn2 connection is being used by the proxies to multiplex
two sessions - Client1<-->Server1 and Client2<-->Server2. Lets think about the two cases we are interested in

##### Backpressure

As we can see here, the problem that we will face is that a client with a bad 1Mbps connection can end
up blocking a fast client with a 1Gbps connection - same principles as explained in the section about 
1:1 connections

```
Proxy1_Reader() {
    for {
        data = read(conn2)
        if data-is-for-client1 {
            write(conn1, data)
        } else if data-is-for-client2 {
            write(conn4, data)
        }
    }
}
```
We can see aboe that if conn1 hangs, then conn4 also hangs ! Now we may think that adding a queue in between
like the two thread model will solve the problem here, lets look at that

```
Queue1 = make_Queue(1Mb)
Queue2 = make_Queue(1Mb)

Proxy1_Reader() {
    for {
        data = read(conn2)
        if data-is-for-client1 {
            Queue1.enqueue(data)
        } else if data-is-for-client2 {
            Queue2.enqueue(data)
        }
    }
}

Proxy1_Writer_Client1() {
    for {
        data = Queue1.dequeue()
        write(conn1, data)
    }
}

Proxy1_Writer_Client2() {
    for {
        data = Queue1.dequeue()
        write(conn4, data)
    }
}
```

As in the previous 1:1 section discussion, we can see that eventually the slow Client1 queue will fill
up and the reader thread will block anyways. So one can ask "why not just drop the client1 packet instead of blocking".
Like we discussed earlier, we cant just drop! We have to close the session - which means the client1 session
will get closed ! Now again one may argue that "its ok to close, its pbbly a rare scenario", but the answer
is its not a rare scenario, it will be a VERY COMMON scenario. Lets take the speedtest example again, speedtest.net
will dowload data at 1gbps all the way to proxy1 and hence it is GUARANTEED that the proxy1 Queue1 will fill up
every time we do a speedtest ! So if drop the packet, then every single speedtest session will get closed and 
speedtest will never complete.

**NOTE: A DESIGN CHOICE CONCLUSION: If we multiplex sessions in a proxy, a slow session can block a fast session.
Additional queueing / multi threading does not help, it just causes buffer bloat and then ends up blocking anyways.**

###### HTTP2 and multiplexed streams

HTTP2 has the ability to multiplex streams inside it with the provision for backpressuring individual stream.
So one can ask, why not replace the conn2 between Proxy1 and Proxy2 with an http2 session and then maybe we 
can backpressure the Client1 and Client2 sessions independently. In theory that sounds good, but HTTP2 is fundamentally
a mechanism meant for carrying multiple sessions between the SAME ENDPOINTS using one tcp session. That is, 
the use case is for a browser opening multiple sessions to a particular server, and all of them being sent via
one TCP session. So the individual sessions have exactly the same speed / bandwitdh / latency / loss parameters.
But in the case of our proxy, even if conn2 is http2, conn1 and conn4 are EXTREMELY DIFFERENT in terms of loss, latency
bandwidth etc.. And if we start digging into the detals of HTTP2 PAUSE frames and other mechanisms for backpressure,
we can see that HTTP2 wont work too well in the proxy scenario - its just not designed for this use case!

##### Packet drops

In the multiplexed case, Proxy2 cant really close conn2 in the picture above if it ended up dropping packets 
corresponding to Server2, becuase that can also affect Client1. So Proxy2 will close Server2 connection and 
send a message back to Proxy1 saying that the drop was for a packet corresponding to client1, so that proxy1
can close the Client1 connection. So in summary, the multiplexed session will end up having to resort to signalling
but crafting a packet indicating what session to close etc..

## Nextensio and connections

Using the light from the above discussions, let us see how we can translate those learnings to the nextensio
design. Lets look at a single cluster case first as below

```
Agent1---conn1----Minion1==[conn2, conn3]==Minion2---[conn4, conn5]---Connector1
                  |                        
Agent2---conn6----+                        
```

All the learnings tell us that if we multiplex all the agents using one session between the minions, that will 
result in slow agent blocking fast agent. So each agent that wants to talk from minion to minion will end up
opening their own sessions. So in the above picture, conn1 will map to conn2 and conn6 will map to conn3

Similarly, going from minion to connector will have the same multiplexing / head of line blocking issue. So 
each agent that talks via a connector will get their own connections. so conn1 will map to conn4 and 
conn6 will map to conn5 in the above picture. 

Now one can say that the agents are multiplexing multiple applications / tcp sessions on conn1 and conn6, so
wont they have head of line blocking issues ? The answer is yes they can have. The BEST solution is if we can
open sessions from agent to minion mapped 1:1 to application sessions. That might end up being a scale challenge
on the minions, but we will design our agents/minion such that we expect a flexible number of sessions (more than 1)
from the agent to the minion - a reasonable tradeoff might be that say we open four sessions and then map different
applications to these four sessions, so we get some degress of fairness across the applications

Now coming to the multi cluster scenario below

```
Agent1---conn1----Minion1==[conn3, conn4]===Remote cluster
                  |                        
Agent2---conn2----+  
```

Here again, the minion will open unique sessions per agent to the remote cluster so that a slow agent does not
head of line block a fast agent. Also please refer the section before about HTTP2 - using HTTP2 tunnel between
the clusters (instead of independent conn3, conn4 sessions) will not really solve the problem.

### Nextensio and threads (goroutines)

As we discussed before in detail in the 1:1 / Multiplexed session discussions, multiple threads a queues in between
the readers and writers really does not solve any problems, it just delays the problem with the additional disadvantage
that it adds to the memory pressure which according to all the research on buffer bloat only degrades performance.
So in the code here, we will have individual threads/goroutines for individual connections - like conn1 and conn6 
above will get seperate threads/goroutines, but the reader threads/goroutines will also do the write from the same
thread/goroutine. So conn1 goroutine will try to write to conn4 in the above example and if that goroutine gets 
blocked it does not affect the conn6 goroutine which runs seperately (writing to conn3)

### Nextensio and packet drops

```
Agent1---conn1----Minion1==[conn2]==Minion2---conn3---Connector1
                                              conn4---Connector2
```    

In the above picture, note that between Agent1 and Minion1, the connection conn1 is multiplexing multiple tcp 
streams - lets say one application goes via connector1 and another goes via connector2.

Now lets say Minion2 to Connector2 tunnel went down and hence the packets have to be dropped. But that does
not mean that we can bring down the conn2 session between Minion1 and Minion2 because thats also multiplexing
sessions to Connector1. So all we can do is to send a message back from Minion2 to Minion1 say that "all the
sessions going to connector2 need to be terminated" and Minion1 relays that message back to the Agent saying
"these sessions A, B, C, D need to be closed" - the agent has no idea about "connectors", so agent needs to
be told about the exact sessions. Its a TBD on how exactly we will implement this, who will keep track of what
sessions are on what connectors etc.. This certainly needs a lot more thinking and design.

### Nextension and socket read/write timeouts

Read timeouts are absolutely not required, we block till there is data thats it !!! Unix systems have evolved
to be perfect enough to ensure that the ONLY reason to block is lack of data, any kind of error will come out
of the blocking state with an error

Write timeouts are also unnecessary, if we ask a tcp socket to write a data, the ONLY reason it blocks is 
because it cant transmit it, like not enough TCP buffer/window size etc.. Again Unix systems have evolved to
perfection in that matter, there is no case of "uknown hang/block", any kind of errors will come out of the
block with an error. And given our threading model where we have a goroutine per reader and we have a write
socket 1:1 corresponding to the reader, a "read block if no data" "write block if cant transmit, leading to
automatic read backpressure" are both the correct things to do.

So to re-emphasise, we DO NOT want any kind of read/write socket timeouts to be configured