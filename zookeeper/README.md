# Zookeeper

## Running zookeeper: static config

Just do as follows:
```console
$ kubectl label node nodename1 role=server1
$ kubectl label node nodename2 role=server2
$ kubectl create -f zookeeper.yaml
```

This will create a zookeeper ensemble statically configured. Killing a pod will bring it back on the same node. It will use the same hostPath as its datadir.

### Running zookeeper: controller

You can also run the controller, if you have some recent version of Kubernetes (since it requires the kubernetes api/client package):
```
$ go build main.go
$ ./main
...
$ curl localhost:8080/scale?replicas=3
```

Every scale event will cause a destruction and reconstruction of the entire ensemble, but the members will come up with the same data.

## Notes on zookeeper

Znode: data + statistical and versioning information, 1Mb limit.
* persistent: db config
* ephemeral: service discovery (new svc creats ephemeral node, deleted when session times out).
* sequential: adds a rv to a node, can be persistent or ephemeral.

Ensemble: leader/follower/observer.

Config:
* tickTime: ms, unit for other timeouts
* initLimit: a quorum member has 20s to download data, timeout on initial connection. Quorum member is disconnected if it can't dl in this time.
* syncLimit: if a follower can't connect to a leader in this time it's a partition. if all followers can't find leader, they re-elect.
* clientPort: 2181
* server list indices go from 1-255, eg:
    - server.1: master:p2p-port(2888),leader-election-port(3888)

Myid: contains the index in the server list above. Located in the datadir specified in conf.
