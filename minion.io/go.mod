module minion.io

go 1.15

// This ideally should need to be done only in the common repo because thats where we use gvisor
// But when we require common here, the replace statement that already exists in common does not
// seem to be honored/inherited and hence we are having to repeat it here. The gvisor lib has
// a couple of fixes required for android and hence we have forked it into our own repo and added
// the couple of fixes on top
replace gvisor.dev/gvisor v0.0.0-20201204040109-0ba39926c86f => github.com/nextensio/gvisor v0.0.0-20210204213648-2e0adbf0d94a

require (
	github.com/google/uuid v1.1.2
	github.com/miekg/dns v1.1.31
	github.com/open-policy-agent/opa v0.23.2
	github.com/opentracing/opentracing-go v1.2.0
	github.com/prometheus/client_golang v0.8.0
	github.com/softlayer/softlayer-go v1.0.3
	github.com/uber/jaeger-client-go v2.29.1+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible
	gitlab.com/nextensio/common v0.0.0-20210823213928-d19c3ae409b9 // indirect
	gitlab.com/nextensio/common/go v0.0.0-20210907221926-2e49b198400b
	go.mongodb.org/mongo-driver v1.4.1
	go.uber.org/zap v1.15.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)
