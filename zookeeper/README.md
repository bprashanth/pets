Znode: data + statistical and versioning information, 1Mb limit.
    - persistent: db config
    - ephemeral: service discovery (new svc creats ephemeral node, deleted when session times out).
    - sequential: adds a rv to a node, can be persistent or ephemeral.

Ensemble: leader/follower/observer.

Config:
    tickTime: ms, unit for other timeouts
    initLimit: a quorum member has 20s to download data, timeout on initial connection. Quorum member is disconnected if it can't dl in this time.
    syncLimit: if a follower can't connect to a leader in this time it's a partition. if all followers can't find leader, they re-elect.
    clientPort: 2181
    server.1: master:p2p-port(2888),leader-election-port(3888)
    server.2: master:p2p-port,leader-election-port
    server.3: master:p2p-port,leader-election-port
Myid:
    contains the index in the server list above.
