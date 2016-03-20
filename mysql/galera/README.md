Why does this example focus on Galera over clustered MYSQL with NDB?

Galera is simpler to reason about, the key differences from some curory research:
* Replication: Galera replicates the entire DB, NDB partitions the dataset and applies a replication factor.
* Loadbalancing: Galera doesn't loadbalance, you need to connect to a specific host, and all hosts have the same data. NDB appears to manage read throughput by being smarter about sending requests to backends where the right stripe of data resides.
* Scaling: Adding more nodes will probably increase latency for Galera (even though writes are in parallel), probably won't for NDB (in fact it will probably increase read throughput).
* Failure: both solutions rely on timeouts and heartbeats, but a single failing node impacts ALL writes in Galera, i.e a node could go down in NDB and not affect an ongoing commit because the cluster is "up" as long as a single node is up and running in each node group.
* Fault tolerance: Galera can withstand all but one node going down. NDB only strips across 2 by default, if a node group is completely dead the cluster will shutdown.

So essentially, we never want to shutdown the cluster if 2 replicas go down, and it's OK if a single commit during container death takes a little longer.

Noteworthy points about Galera:
* SST(Single State Transfer): purge data, copy from a peer. Really slow for O(100 GB) databases. On a gigabit n/w this will take O(30m).
* Normal case only the diff of the binlog index is sent over the network.


