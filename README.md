**Cluster Software**
- minion       : handles forwarding of packets - written in python
- nxts.io      : handles forwarding of packets - written in go
- istio_client : test software and certificates
- istio_yaml   : Yaml files for gateway, virtual service
- docs         : Describes how the minion interacts with agent and connector
- mocklib      : Build mock libraries needed by minion code written in python

**Pushing docker images to registry**

- make
- docker login registry.gitlab.com
- docker push registry.gitlab.com/nextensio/cluster/minion:0.80
