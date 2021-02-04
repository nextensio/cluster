module minion.io

go 1.15

replace gvisor.dev/gvisor v0.0.0-20201204040109-0ba39926c86f => github.com/gopakumarce/gvisor v0.0.0-20210204213648-2e0adbf0d94a

require (
	github.com/google/uuid v1.1.2
	github.com/miekg/dns v1.1.31
	github.com/open-policy-agent/opa v0.23.2
    gitlab.com/nextensio/common v0.0.0-20210204214244-116cd32dc327
	go.mongodb.org/mongo-driver v1.4.1
	go.uber.org/zap v1.15.0
	gopkg.in/natefinch/lumberjack.v2 v2.0.0
)
