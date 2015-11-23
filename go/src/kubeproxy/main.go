package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	kapi "k8s.io/kubernetes/pkg/api"
	kcache "k8s.io/kubernetes/pkg/client/cache"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	kframework "k8s.io/kubernetes/pkg/controller/framework"
	kselector "k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/util"
)

// goproxy main purpose is to be a daemon connecting the docker daemon
// (remote API) and the custom Minecraft server (cubrite using lua scripts).
// But the cuberite lua scripts also execute goproxy as a short lived process
// to send requests to the goproxy daemon. If the program is executed without
// additional arguments it is the daemon "mode".
//
// As a daemon, goproxy listens for events from the docker daemon and send
// them to the cuberite server. It also listen for requests from the
// cuberite server, convert them into docker daemon remote API calls and send
// them to the docker daemon.

// instance of DockerClient allowing for making calls to the docker daemon
// remote API

var (
	argKubecfgFile   = flag.String("kubecfg-file", "", "Location of kubecfg file for access to kubernetes master service; --kube_master_url overrides the URL part of this; if neither this nor --kube_master_url are provided, defaults to service account tokens")
	argKubeMasterURL = flag.String("kube-master-url", "", "URL to reach kubernetes master. Env variables in this flag will be expanded.")
	client           *kclient.Client
)

const (
	// Resync period for the kube controller loop.
	resyncPeriod = 30 * time.Minute
)

// CPUStats Information
type CPUStats struct {
	TotalUsage  uint64
	SystemUsage uint64
}

// A cache that contains all the servicess in the system.
var podsStore kcache.Store

// previousCPUStats is a map containing the previous CPU stats we got from the
// docker daemon through the docker remote API
var previousCPUStats = make(map[string]*CPUStats)

func main() {

	// Create k8s client
	kubeClient, err := newKubeClient()
	if err != nil {
		logrus.Fatal("Failed to create a kubernetes client:", err)
	}
	client = kubeClient

	// goproxy is executed as a short lived process to send a request to the
	// goproxy daemon process
	if len(os.Args) > 1 {
		// If there's an argument
		// It will be considered as a path for an HTTP GET request
		// That's a way to communicate with goproxy daemon
		if len(os.Args) == 2 {
			reqPath := "http://127.0.0.1:8000/" + os.Args[1]
			resp, err := http.Get(reqPath)
			if err != nil {
				logrus.Println("Error on request:", reqPath, "ERROR:", err.Error())
			} else {
				logrus.Println("Request sent", reqPath, "StatusCode:", resp.StatusCode)
			}
		}
		return
	}

	// start a http server and listen on local port 8000
	go func() {
		http.HandleFunc("/containers", listContainers)
		http.HandleFunc("/exec", execCmd)
		http.ListenAndServe(":8000", nil)
	}()

	logrus.Print("about to watch pods")
	podsStore = watchPods(kubeClient)
	logrus.Print("watching pods")
	// wait for interruption
	<-make(chan int)

	logrus.Print("eof")
}

func expandKubeMasterURL() (string, error) {
	parsedURL, err := url.Parse(os.ExpandEnv(*argKubeMasterURL))
	if err != nil {
		return "", fmt.Errorf("failed to parse --kube_master_url %s - %v", *argKubeMasterURL, err)
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" || parsedURL.Host == ":" {
		return "", fmt.Errorf("invalid --kube_master_url specified %s", *argKubeMasterURL)
	}
	return parsedURL.String(), nil
}

func newKubeClient() (*kclient.Client, error) {
	var (
		config    *kclient.Config
		err       error
		masterURL string
	)

	*argKubecfgFile = os.Getenv("KUBE_CFG_FILE")

	logrus.Print("kubeconfig: ", argKubecfgFile)

	if *argKubeMasterURL != "" {
		masterURL, err = expandKubeMasterURL()

		if err != nil {
			return nil, err
		}
	}

	if masterURL != "" && *argKubecfgFile == "" {
		config = &kclient.Config{
			Host:    masterURL,
			Version: "v1",
		}
	} else {
		overrides := &kclientcmd.ConfigOverrides{}
		overrides.ClusterInfo.Server = masterURL
		rules := &kclientcmd.ClientConfigLoadingRules{ExplicitPath: *argKubecfgFile}
		if config, err = kclientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig(); err != nil {
			return nil, err
		}
	}

	logrus.Print("Using ", config.Host, " for kubernetes master")
	logrus.Print("Using kubernetes API", config.Version)
	return kclient.New(config)
}

// Returns a cache.ListWatch that gets all changes to pods.
func createEndpointsPodLW(kubeClient *kclient.Client) *kcache.ListWatch {
	return kcache.NewListWatchFromClient(kubeClient, "pods", kapi.NamespaceAll, kselector.Everything())
}

func watchPods(kubeClient *kclient.Client) kcache.Store {
	eStore, eController := kframework.NewInformer(
		createEndpointsPodLW(kubeClient),
		&kapi.Pod{},
		resyncPeriod,
		kframework.ResourceEventHandlerFuncs{
			AddFunc: handlePodCreate,
			UpdateFunc: func(oldObj, newObj interface{}) {
				handlePodUpdate(oldObj, newObj)
			},
			DeleteFunc: handlePodDelete,
		},
	)

	go eController.Run(util.NeverStop)
	return eStore
}

func handleGenericPodEvent(pod *kapi.Pod) {
	var repo = ""
	var tag = ""
	//TODO: Which container to use?
	containerName := pod.ObjectMeta.Name

	data := url.Values{
		"action":    {"createContainer"},
		"id":        {containerName},
		"name":      {containerName},
		"imageRepo": {repo},
		"imageTag":  {tag}}

	CuberiteServerRequest(data)
}

func handlePodCreate(obj interface{}) {
	if e, ok := obj.(*kapi.Pod); ok {
		handleGenericPodEvent(e)
	}
}

func handlePodUpdate(old interface{}, new interface{}) {
	// oldPod, okOld := old.(*kapi.Pod)
	// newPod, okNew := new.(*kapi.Pod)

	return
}

func handlePodDelete(obj interface{}) {
	if e, ok := obj.(*kapi.Pod); ok {
		//TODO: Which container to use?
		containerName := e.ObjectMeta.Name

		data := url.Values{
			"action": {"destroyContainer"},
			"id":     {containerName},
		}

		CuberiteServerRequest(data)
	}
}

// // eventCallback receives and handles the docker events
// func eventCallback(event *dockerclient.Event, ec chan error, args ...interface{}) {
// 	logrus.Debugln("--\n%+v", *event)
//
// 	id := event.Id
//
// 	switch event.Status {
// 	case "create":
// 		logrus.Debugln("create event")
//
// 		repo, tag := splitRepoAndTag(event.From)
// 		containerName := "<name>"
// 		containerInfo, err := DOCKER_CLIENT.InspectContainer(id)
// 		if err != nil {
// 			logrus.Print("InspectContainer error:", err.Error())
// 		} else {
// 			containerName = containerInfo.Name
// 		}
//
// 		data := url.Values{
// 			"action":    {"createContainer"},
// 			"id":        {id},
// 			"name":      {containerName},
// 			"imageRepo": {repo},
// 			"imageTag":  {tag}}
//
// 		CuberiteServerRequest(data)
//
// 	case "start":
// 		logrus.Debugln("start event")
//
// 		repo, tag := splitRepoAndTag(event.From)
// 		containerName := "<name>"
// 		containerInfo, err := DOCKER_CLIENT.InspectContainer(id)
// 		if err != nil {
// 			logrus.Print("InspectContainer error:", err.Error())
// 		} else {
// 			containerName = containerInfo.Name
// 		}
//
// 		data := url.Values{
// 			"action":    {"startContainer"},
// 			"id":        {id},
// 			"name":      {containerName},
// 			"imageRepo": {repo},
// 			"imageTag":  {tag}}
//
// 		// Monitor stats
// 		DOCKER_CLIENT.StartMonitorStats(id, statCallback, nil)
// 		CuberiteServerRequest(data)
//
// 	case "stop":
// 		// die event is enough
// 		// http://docs.docker.com/reference/api/docker_remote_api/#docker-events
//
// 	case "restart":
// 		// start event is enough
// 		// http://docs.docker.com/reference/api/docker_remote_api/#docker-events
//
// 	case "kill":
// 		// die event is enough
// 		// http://docs.docker.com/reference/api/docker_remote_api/#docker-events
//
// 	case "die":
// 		logrus.Debugln("die event")
//
// 		// same as stop event
// 		repo, tag := splitRepoAndTag(event.From)
// 		containerName := "<name>"
// 		containerInfo, err := DOCKER_CLIENT.InspectContainer(id)
// 		if err != nil {
// 			logrus.Print("InspectContainer error:", err.Error())
// 		} else {
// 			containerName = containerInfo.Name
// 		}
//
// 		data := url.Values{
// 			"action":    {"stopContainer"},
// 			"id":        {id},
// 			"name":      {containerName},
// 			"imageRepo": {repo},
// 			"imageTag":  {tag}}
//
// 		CuberiteServerRequest(data)
//
// 	case "destroy":
// 		logrus.Debugln("destroy event")
//
// 		data := url.Values{
// 			"action": {"destroyContainer"},
// 			"id":     {id},
// 		}
//
// 		CuberiteServerRequest(data)
// 	}
// }

// // statCallback receives the stats (cpu & ram) from containers and send them to
// // the cuberite server
// func statCallback(id string, stat *dockerclient.Stats, ec chan error, args ...interface{}) {
//
// 	// logrus.Debugln("STATS", id, stat)
// 	// logrus.Debugln("---")
// 	// logrus.Debugln("cpu :", float64(stat.CpuStats.CpuUsage.TotalUsage)/float64(stat.CpuStats.SystemUsage))
// 	// logrus.Debugln("ram :", stat.MemoryStats.Usage)
//
// 	memPercent := float64(stat.MemoryStats.Usage) / float64(stat.MemoryStats.Limit) * 100.0
// 	var cpuPercent float64
// 	if preCPUStats, exists := previousCPUStats[id]; exists {
// 		cpuPercent = calculateCPUPercent(preCPUStats, &stat.CpuStats)
// 	}
//
// 	previousCPUStats[id] = &CPUStats{TotalUsage: stat.CpuStats.CpuUsage.TotalUsage, SystemUsage: stat.CpuStats.SystemUsage}
//
// 	data := url.Values{
// 		"action": {"stats"},
// 		"id":     {id},
// 		"cpu":    {strconv.FormatFloat(cpuPercent, 'f', 2, 64) + "%"},
// 		"ram":    {strconv.FormatFloat(memPercent, 'f', 2, 64) + "%"}}
//
// 	CuberiteServerRequest(data)
// }

// execCmd handles http requests received for the path "/exec"
func execCmd(w http.ResponseWriter, r *http.Request) {

	io.WriteString(w, "OK")

	go func() {
		cmd := r.URL.Query().Get("cmd")
		cmd, _ = url.QueryUnescape(cmd)
		arr := strings.Split(cmd, " ")
		if len(arr) > 0 {
			cmd := exec.Command(arr[0], arr[1:]...)
			// Stdout buffer
			// cmdOutput := &bytes.Buffer{}
			// Attach buffer to command
			// cmd.Stdout = cmdOutput
			// Execute command
			// printCommand(cmd)
			err := cmd.Run() // will wait for command to return
			if err != nil {
				logrus.Println("Error:", err.Error())
			}
		}
	}()
}

// listContainers handles and reply to http requests having the path "/containers"
func listContainers(w http.ResponseWriter, r *http.Request) {

	// answer right away to avoid dead locks in LUA
	io.WriteString(w, "OK")

	go func() {
		pods, err := client.Pods("default").List(labels.Everything(), kselector.Everything())

		logrus.Print("made pods request")

		if err != nil {
			logrus.Println(err.Error())
			return
		}

		//images, err := DOCKER_CLIENT.ListImages(true)

		// if err != nil {
		// 	logrus.Println(err.Error())
		// 	return
		// }

		for i := 0; i < len(pods.Items); i++ {

			logrus.Print("got pod:", pods.Items[i].ObjectMeta.Name)

			id := pods.Items[i].ObjectMeta.Name
			//info := "" //, _ := DOCKER_CLIENT.InspectContainer(id)
			name := pods.Items[i].ObjectMeta.Name
			imageRepo := ""
			imageTag := ""

			// for _, image := range images {
			// 	if image.Id == info.Image {
			// 		if len(image.RepoTags) > 0 {
			// 			imageRepo, imageTag = splitRepoAndTag(image.RepoTags[0])
			// 		}
			// 		break
			// 	}
			// }

			data := url.Values{
				"action":    {"containerInfos"},
				"id":        {id},
				"name":      {name},
				"imageRepo": {imageRepo},
				"imageTag":  {imageTag},
				"running":   {"true"},
			}

			CuberiteServerRequest(data)

			// if info.State.Running {
			// 	// Monitor stats
			// 	DOCKER_CLIENT.StartMonitorStats(id, statCallback, nil)
			// }
		}
	}()
}

// Utility functions

// func calculateCPUPercent(previousCPUStats *CPUStats, newCPUStats *dockerclient.CpuStats) float64 {
// 	var (
// 		cpuPercent = 0.0
// 		// calculate the change for the cpu usage of the container in between readings
// 		cpuDelta = float64(newCPUStats.CpuUsage.TotalUsage - previousCPUStats.TotalUsage)
// 		// calculate the change for the entire system between readings
// 		systemDelta = float64(newCPUStats.SystemUsage - previousCPUStats.SystemUsage)
// 	)
//
// 	if systemDelta > 0.0 && cpuDelta > 0.0 {
// 		cpuPercent = (cpuDelta / systemDelta) * float64(len(newCPUStats.CpuUsage.PercpuUsage)) * 100.0
// 	}
// 	return cpuPercent
// }
//
// func splitRepoAndTag(repoTag string) (string, string) {
//
// 	repo := ""
// 	tag := ""
//
// 	repoAndTag := strings.Split(repoTag, ":")
//
// 	if len(repoAndTag) > 0 {
// 		repo = repoAndTag[0]
// 	}
//
// 	if len(repoAndTag) > 1 {
// 		tag = repoAndTag[1]
// 	}
//
// 	return repo, tag
// }

// CuberiteServerRequest send a POST request that will be handled
// by our Cuberite Docker plugin.
func CuberiteServerRequest(data url.Values) {
	client := &http.Client{}
	req, _ := http.NewRequest("POST", "http://127.0.0.1:8080/webadmin/Docker/Docker", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("admin", "admin")
	client.Do(req)
}
