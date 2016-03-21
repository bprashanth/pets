package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	flag "github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/api"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	kubectl_util "k8s.io/kubernetes/pkg/kubectl/cmd/util"
	"k8s.io/kubernetes/pkg/labels"
)

const (
	hostnameKey          = "pod.beta.kubernetes.io/hostname"
	subdomainKey         = "pod.beta.kubernetes.io/subdomain"
	zkSubdomain          = "zk"
	zkDataDir            = "/tmp/zookeeper"
	zkHostPath           = "/var/lib/zookeeper"
	zkImage              = "jplock/zookeeper"
	zkPeerPort           = 2888
	zkLeaderElectionPort = 3888
	nodeSelKey           = "server"
	scaleServerPort      = 8080
)

var (
	flags = flag.NewFlagSet("", flag.ContinueOnError)

	outsideCluster = flags.Bool("running-outside-cluster", true, `If true, use kubectl auth. set if
                running this binary on your local computer.`)

	replicas   = flags.Int("start-replicas", 2, `number of replicas to start with. Updated via :8080/scale?replicas=X`)
	zkAppLabel = map[string]string{"app": "zookeeper"}
)

type lockedCount struct {
	l     sync.Mutex
	count int
}

func (l *lockedCount) get() int {
	l.l.Lock()
	defer l.l.Unlock()
	return l.count
}

func (l *lockedCount) set(c int) {
	l.l.Lock()
	defer l.l.Unlock()
	l.count = c
}

// nodeReady returns true if the given Node is ready for scheduling.
func nodeReady(node api.Node) bool {
	if node.Spec.Unschedulable == true {
		return false
	}
	for ix := range node.Status.Conditions {
		condition := &node.Status.Conditions[ix]
		if condition.Type == api.NodeReady {
			return condition.Status == api.ConditionTrue
		}
	}
	return false
}

// podStorageAllocator allocates storage for the given pod.
// The same pod must get the same storage.
type podStorageAllocator interface {
	alloc(p *api.PodTemplateSpec) (*api.PodTemplateSpec, error)
}

// nodeStorage trivially implements podStorageAllocator.
type nodeStorage struct {
	client *client.Client
}

// labelNode applies value to a node that doesn't already have the key as a
// label (regardless of value).
func (n *nodeStorage) labelNode(key, value string) error {
	nodeList, err := n.client.Nodes().List(api.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodeList.Items {
		// TODO: If the node has the label but isn't ready, try to find another?
		if !nodeReady(node) {
			log.Printf("Ignoring not-ready node %v", node.Name)
			continue
		}

		if v, ok := node.Labels[key]; ok {
			if v == value {
				log.Printf("Node %v already labelled with %v", node.Name, v)
				return nil
			}
			continue
		}

		log.Printf("Labelling node %v %v=%v", node.Name, key, value)
		node.Labels[key] = value
		if _, err := n.client.Nodes().Update(&node); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("Failed to find a node without key %v", key)
}

func (n *nodeStorage) alloc(p *api.PodTemplateSpec) (*api.PodTemplateSpec, error) {

	// TODO: Colliding annotations?
	nodeSelValue := p.Annotations[hostnameKey]
	if err := n.labelNode(nodeSelKey, nodeSelValue); err != nil {
		return nil, err
	}
	p.Spec.NodeSelector[nodeSelKey] = nodeSelValue

	// TODO: With containers > 1 they duke it out inside datadir?
	for i := range p.Spec.Containers {
		p.Spec.Containers[i].VolumeMounts = []api.VolumeMount{
			{
				Name:      "datadir",
				MountPath: zkDataDir,
			},
		}
	}
	p.Spec.Volumes = []api.Volume{
		{
			Name: "datadir",
			VolumeSource: api.VolumeSource{
				HostPath: &api.HostPathVolumeSource{Path: zkHostPath},
			},
		},
	}

	return p, nil
}

// templater generates RC templates.
type templater struct {
	zkCfgDir             string
	zkDataDir            string
	zkPeerPort           int
	zkLeaderElectionPort int
	zkSubdomain          string
	zkImage              string
}

func (t templater) serverList(n int) string {
	serverStr := ""
	for i := 1; i <= n; i++ {
		serverStr = fmt.Sprintf("%vserver.%d=s%d.%v.%v.svc.cluster.local:%d:%d\\n",
			serverStr, i, i, t.zkSubdomain, api.NamespaceDefault, t.zkPeerPort, t.zkLeaderElectionPort)
	}
	return fmt.Sprintf("\"%v\"", serverStr)
}

func (t templater) serverID(i int) string {
	return fmt.Sprintf("echo %d > /tmp/zookeeper/myid", i)
}

func (t templater) newTemplatedSvc() *api.Service {
	return &api.Service{
		ObjectMeta: api.ObjectMeta{
			Name:   t.zkSubdomain,
			Labels: zkAppLabel,
		},
		Spec: api.ServiceSpec{
			ClusterIP: api.ClusterIPNone,
			Selector:  zkAppLabel,
			Ports: []api.ServicePort{
				{Name: "peer", Port: t.zkPeerPort},
				{Name: "leader-election", Port: t.zkLeaderElectionPort},
			},
		},
	}
}

func (t templater) newTemplatedRC(i, n int) *api.ReplicationController {

	// TODO: Write out a template file instead of this hackery.
	cmd := fmt.Sprintf("echo -e %v >> /opt/zookeeper/conf/zoo.cfg; %v; /opt/zookeeper/bin/zkServer.sh start-foreground", t.serverList(n), t.serverID(i))
	log.Printf("RC %d Command %v", i, cmd)
	petName := fmt.Sprintf("zk%d", i)
	return &api.ReplicationController{
		ObjectMeta: api.ObjectMeta{
			Name:   petName,
			Labels: zkAppLabel,
		},
		Spec: api.ReplicationControllerSpec{
			Replicas: 1,
			Selector: map[string]string{
				"server": petName,
				"app":    "zookeeper",
			},
			Template: &api.PodTemplateSpec{
				ObjectMeta: api.ObjectMeta{
					Annotations: map[string]string{
						hostnameKey:  fmt.Sprintf("s%d", i),
						subdomainKey: t.zkSubdomain,
					},
					Labels: map[string]string{
						"server": petName,
						"app":    "zookeeper",
					},
				},
				Spec: api.PodSpec{
					NodeSelector: map[string]string{},
					Containers: []api.Container{
						{
							Name:    "zk",
							Image:   t.zkImage,
							Command: []string{"sh", "-c", cmd},
							Ports:   []api.ContainerPort{{ContainerPort: t.zkPeerPort, Name: "peer"}, {ContainerPort: t.zkLeaderElectionPort, Name: "leader-election"}},
						},
					},
				},
			},
		},
	}
}

func newTemplater() templater {
	return templater{
		zkCfgDir:             "/opt/zookeeper/conf",
		zkDataDir:            "/tmp/zookeeper",
		zkPeerPort:           zkPeerPort,
		zkLeaderElectionPort: zkLeaderElectionPort,
		zkSubdomain:          zkSubdomain,
		zkImage:              zkImage,
	}
}

// zkEnsemble performs a rolling-update-esque operation everytime the ensemble grows/shrinks.
// TODO: Zookeeper 3.5.0 allows dynamic reconfig: https://zookeeper.apache.org/doc/trunk/zookeeperReconfig.html
type zkEnsemble struct {
	client  *client.Client
	storage podStorageAllocator
	t       templater
	labels  map[string]string
}

func (z *zkEnsemble) checkSvc() error {
	svc := z.t.newTemplatedSvc()
	if _, err := z.client.Services(api.NamespaceDefault).Get(svc.Name); err == nil {
		return nil
	}
	log.Printf("Creating service %v", svc.Name)
	_, err := z.client.Services(api.NamespaceDefault).Create(svc)
	return err
}

func (z *zkEnsemble) sync(n int) error {
	if err := z.checkSvc(); err != nil {
		return err
	}

	selector := labels.SelectorFromSet(labels.Set(z.labels))
	rl, err := z.client.ReplicationControllers(api.NamespaceDefault).List(api.ListOptions{LabelSelector: selector})
	if err != nil {
		log.Printf("Failed to list rcs %v", err)
		return err
	}
	if len(rl.Items) == n {
		log.Printf("Ensemble already has %d members", n)
		return nil
	}

	rcList := []*api.ReplicationController{}
	// myid range if from 1-255
	for i := 1; i <= n; i++ {
		rc := z.t.newTemplatedRC(i, n)
		rc.Spec.Template, err = z.storage.alloc(rc.Spec.Template)
		if err != nil {
			return err
		}
		rcList = append(rcList, rc)
	}

	// Don't delete rcs till we've allocated storage for them all.
	for _, rc := range rcList {
		// TODO: handle errors better. A legit get error will show up during create, hopefully.
		if _, err := z.client.ReplicationControllers(api.NamespaceDefault).Get(rc.Name); err == nil {
			log.Printf("Deleting rc %v", rc.Name)
			if err := z.client.ReplicationControllers(api.NamespaceDefault).Delete(rc.Name); err != nil {
				return err
			}
		}
		if _, err := z.client.ReplicationControllers(api.NamespaceDefault).Create(rc); err != nil {
			return err
		} else {
			log.Printf("Created %v", rc.Name)
		}
	}
	return nil
}

func main() {
	clientConfig := kubectl_util.DefaultClientConfig(flags)
	flags.Parse(os.Args)

	var kubeClient *client.Client
	var err error
	if *outsideCluster {
		config, err := clientConfig.ClientConfig()
		if err != nil {
			log.Fatalf("error connecting to the client: %v", err)
		}
		kubeClient, err = client.New(config)
	} else {

		kubeClient, err = client.NewInCluster()
		if err != nil {
			log.Fatalf("Failed to create client: %v.", err)
		}

	}
	// TODO: Create zookeeper service.

	counter := &lockedCount{count: *replicas}
	http.HandleFunc("/scale", func(w http.ResponseWriter, r *http.Request) {
		if num, err := strconv.Atoi(r.URL.Query().Get("replicas")); err != nil {
			log.Fatalf("Failed to parse %q", r)
		} else {
			counter.set(num)
			w.WriteHeader(200)
			w.Write([]byte(fmt.Sprintf("Scaling to %d", counter.get())))
		}
	})
	go func() {
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%v", scaleServerPort), nil))
	}()

	z := zkEnsemble{storage: &nodeStorage{kubeClient}, client: kubeClient, labels: zkAppLabel, t: newTemplater()}
	for {
		if err := z.sync(counter.get()); err != nil {
			log.Printf("Failed to sync zk ensemble %v, will try again in 5s", err)
		}
		time.Sleep(5 * time.Second)
	}
}
