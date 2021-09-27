module minion.io

go 1.15

// This ideally should need to be done only in the common repo because thats where we use gvisor
// But when we require common here, the replace statement that already exists in common does not
// seem to be honored/inherited and hence we are having to repeat it here. The gvisor lib has
// a couple of fixes required for android and hence we have forked it into our own repo and added
// the couple of fixes on top
replace gvisor.dev/gvisor v0.0.0-20201204040109-0ba39926c86f => github.com/nextensio/gvisor v0.0.0-20210204213648-2e0adbf0d94a

require (
	github.com/HdrHistogram/hdrhistogram-go v1.1.2 // indirect
	github.com/go-stack/stack v1.8.1 // indirect
	github.com/google/uuid v1.3.0
	github.com/miekg/dns v1.1.43
	github.com/open-policy-agent/opa v0.32.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/pion/dtls/v2 v2.0.9 // indirect
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/common v0.30.0 // indirect
	github.com/prometheus/procfs v0.7.3 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	github.com/uber/jaeger-client-go v2.29.1+incompatible
	github.com/uber/jaeger-lib v2.4.1+incompatible
	github.com/youmark/pkcs8 v0.0.0-20201027041543-1326539a0a0a // indirect
	gitlab.com/nextensio/common/go v0.0.0-20210927130631-8880ad8af590
	go.mongodb.org/mongo-driver v1.7.2
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	go.uber.org/zap v1.19.1
	golang.org/x/crypto v0.0.0-20210817164053-32db794688a5 // indirect
	golang.org/x/net v0.0.0-20210908191846-a5e095526f91 // indirect
	golang.org/x/sys v0.0.0-20210910150752-751e447fb3d0 // indirect
	golang.org/x/text v0.3.7 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)
