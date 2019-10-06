# istio
Yaml files for nextensio services

Bootstraping k8s/istio cluster for nextensio

1. Copy k8s config file to home directory
    ~/.kube/config
2. Generate cert for gateway
    we need to create common ca-certs for all cluster and then generate server/client certificate for each cluster. These certs needs to be made
    availabe inside the cluster in one of few ways:
     - create a stroage, and keep all the keys then mount this volume in all pods and gateway (needs modification in sidecar configmap and gateway deployments).
     - create a secret in istio amd tjem mount this in all pods and gateway (needs modification in sidecar configmap and gateway deployments).
       -- TODO
    ./gen_cert.sh <gateway> <password>
    ./kubectl --kubeconfig=<config> create -n istio-system secret tls istio-ingressgateway-<region>-nextensio-certs --key <gateway>/3_application/private/<gateway>.key.pem --cert <gateway>/3_application/certs/<gateway>.cert.pem
    ./kubectl --kubeconfig=<config> create -n istio-system secret generic istio-ingressgateway-<region>-nextensio-ca-certs --from-file=<gateway>/2_intermediate/certs/ca-chain.cert.pem
3. Create gateway
    ingress-gateway
        ./k8s_gateway.py --gateway <>
    egress-gateway
        For all other cluster we need to create an entry in egress-gateway. So with N cluster there are
        N enries. This is used to route inter cluster traffic. (TODO)
4. Deploy consul
       - create service entry for each node -- TODO
5. Update coredns to add consul dns for *.service.consul search. Restart coredns. -- TODO
6. Create namespace -- for default namespace, skip this step. sidecar injection is enabled
    ./k8s_ns.py --token <> --ns <namespace> --ca <crt> --host <>
    a. istioctl kube-inject --injectConfigFile ric-inject-config.yaml --meshConfigFile ric-mesh-config.yaml --filename <deploy-t.yaml> | kubectl apply -f -
7. Create POD for tenant services
    Determine whether new POD needs to be created for this tenant service.
    For 1 POD 1 service, decision logic is simple. For mapping N service per POD, we need
    to take into account other factors to decide a new POD is needed or not. For POD naming
    we can take base domain for tenant e.g., say aaa.com, we name pod like aaa.com.pod.<instance_id>.
    For Demo, pod name is same as service name, with 1:1
    Also we need to pass cluster_id to POD during creation (TODO)
    ./k8s_deploy.py --token <> --ns <namespace> --ca <crt> --host <> --pod <tom.com>
    ./k8s_deploy.py --token <> --ns <namespace> --ca <crt> --host <> --pod <candy.com>
    While creating a POD we need to pass environment variables like:
        - cluster id {e.g., sjc, ric, etc)
        - conusl dns ip === MY_DNS=$(kubectl get svc -l app=consul -n consul-system | grep dns | awk '{print $3}')
        - Node IP
        - POD IP
        - POD name
8. Create k8s service for above pods
    Two Ports (k8s service port) are created. Port 80 is used for internal communication.
    Port 8002 is used for connectiong outside k8s entity.
    ./k8s_port.py --token <> --ns <namespace> --ca <crt> --host <> --pod <tom.com>
    ./k8s_port.py --token <> --ns <namespace> --ca <crt> --host <> --pod <candy.com>
9. Create routing rules for internal entries
    Virtual service is created for POD. For each user service goes into match statement. This
    routine needs to support updating virtual service with new match statement (TODO).
    Also we need to add KV pair in consul as 
    {key: user service, value: {cluster id, gateway, pod name}} TODO
    ./k8s_routing --service <tom.com> --pod <tom.com> --gateway <>
    ./k8s_routing --service <candy.com> --pod <candy.com> --gateway <>
    Scalability:
     There are two types of match statements:
      1. x-nextensio-connect
         Its scope is local to a given cluster. This is equal to the number of agent/connector
         served by that cluster
      2. x-nextensio-for
         ITs scope is global (for all cluster). These will go into consul. (TODO)
10. Create routing rules for external cluster (TODO)
        - route all traffic to egress-gateway
        - create service entry for external cluster
        - create destination rule to upgrade http traffic to https
11. Policy rules (TODO)
    Agent/connector are grouped to ID (group tag). These ID will be typically scoped per tenant,
    as we expect tenant policy will be different for tenant to tenant. To support cross tenant
    traffic we can support some global IDs. In istio this will translate into "match statement" and
    needs to program in all clusters.
