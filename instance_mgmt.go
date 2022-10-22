package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/captainGeech42/chaldeploy/internal/generic_map"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type InstanceState int64

const (
	// a Running instance is live and can be accessed by the team
	Running InstanceState = iota

	// a Destroying instance is something in the process of being torn down.
	// From the perspective of the user, it is destroyed.
	// However, from the perspective of the backend, it isn't in a state where
	// it can be recreated.
	Destroying

	// a Destroyed instance doesn't exist anymore, and can be (re)deployed.
	// This is the first state of a DeploymentInstance
	Destroyed
)

func (s InstanceState) String() string {
	switch s {
	case Running:
		return "running"
	case Destroying:
		return "destroying"
	case Destroyed:
		return "destroyed"
	default:
		return "(unknown enum value)"
	}
}

// DeploymentInstance is a single deployment of a challenge for a team
type DeploymentInstance struct {
	// value for the `app` label
	AppName string

	// k8s namespace used for the instance
	Namespace string

	// expiration time for the instance
	// ExpTime string

	// the current state of the instance
	State InstanceState

	// lock for mutating the state of the instance
	mu *sync.Mutex
}

// implement sync.Locker on DeploymentInstance
func (di *DeploymentInstance) Lock() {
	di.mu.Lock()
}

func (di *DeploymentInstance) Unlock() {
	di.mu.Unlock()
}

// InstanceManager stores the necessary data for creating and destroying challenge instances on a k8s cluster
type InstanceManager struct {
	// k8s config
	Config *rest.Config

	// k8s client
	Clientset *kubernetes.Clientset

	// mutex for controlling access to the instance map
	Lock *sync.RWMutex

	// map of team id -> instance
	Instances *generic_map.MapOf[string, *DeploymentInstance]
}

// Initialize the instance manager object, including authing to the cluster
// TODO: ensure necessary permissions are obtained
func (im *InstanceManager) Init() error {
	// load the cluster config
	k8sConfig, err := getConfigForCluster()
	if err != nil {
		return err
	} else {
		im.Config = k8sConfig
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(im.Config)
	if err != nil {
		return err
	} else {
		im.Clientset = clientset
	}

	// initialize the map
	im.Instances = new(generic_map.MapOf[string, *DeploymentInstance])

	return nil
}

// Deploy an instance of a challenge for a team
// Returns the connection string and error
// ref:
//   - https://github.com/kubernetes/client-go/blob/master/examples/in-cluster-client-configuration/main.go
//   - https://github.com/kubernetes/client-go/blob/master/examples/create-update-delete-deployment/main.go
func (im *InstanceManager) CreateDeployment(teamName, teamId string) (string, error) {
	// compute a unique identifer for this deployment
	uniqName := strings.ToLower(fmt.Sprintf("chaldeploy-%s-%s", HashString(config.ChallengeName), strings.ReplaceAll(teamId, "-", "")))

	// initialize the Deployment Instance
	di := DeploymentInstance{
		AppName:   uniqName,
		Namespace: uniqName,
		State:     Destroyed,
	}

	// get the k8s deployment object
	// TODO: get the other k8s objects
	deployment := getDeployment(di.AppName, teamName, teamId)

	// create the k8s objects
	deploymentsClient := im.Clientset.AppsV1().Deployments(di.Namespace)
	_, err := deploymentsClient.Create(context.TODO(), &deployment, metav1.CreateOptions{})
	if err != nil {
		panic(err)
	}

	// save the instance
	di.State = Running
	im.Instances.Store(teamId, &di)

	return "1.2.3.4:9999", nil
}

// Destroy a challenge deployment
func (im *InstanceManager) DestroyDeployment(teamId string) error {
	// get a ptr to the instance
	di, ok := im.Instances.Load(teamId)
	if !ok || di == nil {
		return fmt.Errorf("tried to destroy a non-exist deployment for %s", teamId)
	}

	if di.State == Destroying {
		// deployment is in the process of being destroyed, don't try to destroy it again
		return nil
	}

	// acquire the lock on the deployment and mark it as being destroyed
	di.mu.Lock()
	di.State = Destroying
	di.mu.Unlock()

	// init clients in the right namespace
	deploymentsClient := im.Clientset.AppsV1().Deployments(di.Namespace)

	// check if the resource exists, exit if it doesn't
	if deployment, err := deploymentsClient.Get(context.TODO(), di.AppName, metav1.GetOptions{}); err != nil || deployment == nil {
		// TODO: investigate how err can be set (e.g., failed to lookup vs successfully looked up and confirmed non-existent)
		return nil
	}

	// delete resources
	di.mu.Lock()
	defer di.mu.Unlock()
	deletePolicy := metav1.DeletePropagationForeground

	if err := deploymentsClient.Delete(context.TODO(), di.AppName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		return fmt.Errorf("failed to delete deployment %s: %v", di.AppName, err)
	}

	di.State = Destroyed

	return nil
}

/////////////////////////////////

// get a labelselector object that can be used for the deployment and service objects
func getSelector(appName, teamId string) *metav1.LabelSelector {
	return &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app":                              appName,
			"chaldeploy.captaingee.ch/team-id": teamId,
		},
	}
}

func getNamespace()

// get the deployment struct for the target app
func getDeployment(appName, teamName, teamId string) appsv1.Deployment {
	selector := getSelector(appName, teamId)

	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: appName,
			Labels: map[string]string{
				"app":                                appName,
				"chaldeploy.captaingee.ch/chal-name": config.ChallengeName,
				"chaldeploy.captaingee.ch/target":    "yes",
				"chaldeploy.captaingee.ch/team-name": teamName,
				"chaldeploy.captaingee.ch/team-id":   teamId,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: selector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":                                appName,
						"chaldeploy.captaingee.ch/chal-name": config.ChallengeName,
						"chaldeploy.captaingee.ch/target":    "yes",
						"chaldeploy.captaingee.ch/team-name": teamName,
						"chaldeploy.captaingee.ch/team-id":   teamId,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            strings.Split(config.ChallengeImage, ":")[0],
							Image:           config.ChallengeImage,
							ImagePullPolicy: "Never", // TODO: adjust as needed off of minikube
							Ports:           []corev1.ContainerPort{{ContainerPort: int32(config.ChallengePort)}},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"), // TODO: configify these
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	return deployment
}

// Identify the proper source for the cluster config and load it
// Load order:
//   - $CHALDEPLOY_K8SCONFIG
//   - /var/run/secrets/kubernetes.io/serviceaccount
//   - ~/.kube/config current context
func getConfigForCluster() (*rest.Config, error) {
	// check if a path to the k8s config was specified
	if config.K8sConfigPath != "" {
		log.Printf("using k8s config path from env var: %s\n", config.K8sConfigPath)

		// check if it exists
		if _, err := os.Stat(config.K8sConfigPath); os.IsExist(err) {
			// file exists, try to use it
			k8sConfig, err := clientcmd.BuildConfigFromFlags("", config.K8sConfigPath)
			if err != nil {
				return nil, err
			} else {
				return k8sConfig, nil
			}
		} else {
			return nil, errors.New("specified filepath for k8s config doesn't exist")
		}
	} else {
		// no path was specified, try an injected service account
		if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount"); os.IsExist(err) {
			log.Println("found a service account, using k8s config from it")

			// ref: https://github.com/kubernetes/client-go/blob/master/examples/in-cluster-client-configuration/main.go#L41
			k8sConfig, err := rest.InClusterConfig()
			if err != nil {
				return nil, err
			} else {
				return k8sConfig, nil
			}
		} else {
			// no service account, try ~/.kube/config
			log.Println("service account not found, loading current context from k8s config in home dir")

			// ref: https://github.com/kubernetes/client-go/blob/master/examples/out-of-cluster-client-configuration/main.go#L43
			var configPath string
			if home := homedir.HomeDir(); home != "" {
				configPath = filepath.Join(home, ".kube", "config")
			} else {
				return nil, errors.New("couldn't resolve home directory, can't load local k8s config")
			}

			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				return nil, errors.New("couldn't find a k8s config to load")
			}

			// use the current context in kubeconfig
			k8sConfig, err := clientcmd.BuildConfigFromFlags("", configPath)
			if err != nil {
				return nil, err
			} else {
				return k8sConfig, nil
			}
		}
	}
}
