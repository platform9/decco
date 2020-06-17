package test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/drone/envsubst"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestDecco(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Decco E2E Suite")
}

const (
	testStagingDir            = "./test_staging"
	testStagingManifestsDir   = testStagingDir + "/manifests"
	testDataDir               = "./test_data"
	manifestsDir              = "../manifests"
	testClusterName           = "test-decco" // Note: the name should be lowercase because of constraints in KinD.
	testClusterKubeconfigPath = testStagingDir + "/kubeconfig"
	testClusterVersion        = "v1.16.9"
	testImageTag              = "e2e-test"
	testDeccoOperatorImage    = "platform9/decco-operator:" + testImageTag
)

var _ = Describe("Decco", func() {
	BeforeSuite(func() {
		//
		// Perform prerequisite checks
		//

		ensureCommandExists("kind")
		ensureCommandExists("docker")
		ensureCommandExists("kubectl")
		ensureDirExists(testDataDir)
		ensureDirExists(manifestsDir)

		By("Ensuring that the test data directory exists: " + testStagingDir)
		err := os.MkdirAll(testStagingDir, 0755)
		Expect(err).To(BeNil(), "Failed to create test staging directory %s", testStagingDir)
		err = os.MkdirAll(testStagingManifestsDir, 0755)
		Expect(err).To(BeNil(), "Failed to create test staging manifests directory %s", testStagingManifestsDir)

		//
		// Build the test image
		//

		By("Building the Decco Docker image")
		err = os.Setenv("GOOS", "linux")
		Expect(err).To(BeNil())
		err = os.Setenv("GOARCH", "amd64")
		Expect(err).To(BeNil())
		out, err := execCommand("make", "-C", "..", fmt.Sprintf("IMAGE_TAG=%s", testImageTag), "operator-image")
		Expect(err).To(BeNil(), "Failed to build decco-operator: %s", string(out))

		//
		// Provision the test cluster
		//

		By("Creating a test cluster using KinD")
		out, err = execCommand("kind", "create", "cluster",
			"--name", testClusterName,
			"--kubeconfig", testClusterKubeconfigPath,
			"--image", fmt.Sprintf("kindest/node:%s", testClusterVersion),
			"--wait", "5m")
		Expect(err).To(BeNil(), "Failed to create test KinD cluster: %s", string(out))

		By("Setting KUBECONFIG to the kubeconfig of the test cluster")
		err = os.Setenv("KUBECONFIG", testClusterKubeconfigPath)
		Expect(err).To(BeNil(), "Failed to set KUBECONFIG")

		By("Ensuring that the test cluster is accessible")
		out, err = execCommand("kubectl", "version")
		Expect(err).To(BeNil(), "Failed to connect to test KinD cluster: %s", string(out))
		if err == nil {
			log(string(out))
		}

		By("Making the Decco image available to the test cluster")
		out, err = execCommand("kind", "load", "docker-image", "--name", testClusterName, testDeccoOperatorImage)
		Expect(err).To(BeNil(), "Failed to build decco-operator: %s", string(out))

		//
		// Stage the required manifests
		//
		stageManifest(path.Join(manifestsDir, "decco-serviceaccount.yaml"))
		stageManifest(path.Join(manifestsDir, "decco-clusterrolebinding.yaml"))
		stageManifest(path.Join(testDataDir, "decco-namespace.yaml"))
		stageManifest(path.Join(testDataDir, "decco-operator-secret.yaml"))

		By("Completing and staging the Decco deployment manifest template", func() {
			bs, err := ioutil.ReadFile(path.Join(manifestsDir, "templates/decco-deployment.yaml.tmpl"))
			Expect(err).To(BeNil(), "Failed to read Decco deployment template: %s", string(out))

			substituted, err := envsubst.Eval(string(bs), func(s string) string {
				switch s {
				case "DECCO_OPERATOR_IMAGE_TAG":
					return testDeccoOperatorImage
				default:
					Fail(fmt.Sprintf("unexpected environment variable in Decco deployment template: %s", s))
					return ""
				}
			})
			Expect(err).To(BeNil(), "Failed to substitute Decco deployment template: %s", string(out))

			err = ioutil.WriteFile(path.Join(testStagingManifestsDir, "decco-deployment.yaml"), []byte(substituted), 0644)
			Expect(err).To(BeNil(), "Failed to write substituted Decco deployment template: %s", string(out))
		})

		//
		// Deploy Decco
		//

		By("Creating the decco namespace in the test cluster", func() {
			out, err := execCommand("kubectl", "apply", "--namespace", "decco", "-f", path.Join(testStagingManifestsDir, "decco-namespace.yaml"))
			Expect(err).To(BeNil(), "Failed to deploy Decco: %s", string(out))
		})

		By("Deploying Decco in the test cluster", func() {
			out, err := execCommand("kubectl", "apply", "--namespace", "decco", "-f", testStagingManifestsDir)
			Expect(err).To(BeNil(), "Failed to deploy Decco: %s", string(out))
		})
	})

	AfterSuite(func() {
		if os.Getenv("DECCO_TEST_PRESERVE_CLUSTER") == "" {
			By("Cleaning up the test cluster (to preserve test cluster set DECCO_TEST_PRESERVE_CLUSTER to a non-empty value)")
			out, err := execCommand("kind", "delete", "cluster", "--name", testClusterName, "--kubeconfig", testClusterKubeconfigPath)
			Expect(err).To(BeNil(), "Failed to delete test KinD cluster: %s", string(out))
		} else {
			By("Preserving test KinD cluster: " + testClusterName)
		}

		if os.Getenv("DECCO_TEST_PRESERVE_DATA") == "" && os.Getenv("DECCO_TEST_PRESERVE_CLUSTER") == "" {
			By("Cleaning up temporary test data (to preserve test data set DECCO_TEST_PRESERVE_DATA or DECCO_TEST_PRESERVE_CLUSTER to a non-empty value)")
			err := os.RemoveAll(testStagingDir)
			Expect(err).To(BeNil())
		} else {
			By("Preserving test data: " + testStagingDir)
		}

	})

	Context("Decco deployment", func() {

		It("should have all pods in a 'Running' state", func() {
			Eventually(func() error {
				out, err := execCommand("kubectl", "--namespace", "decco", "get", "pods", "--no-headers=true")
				if err != nil {
					wrappedErr := fmt.Errorf("failed to get pods: %s", string(out))
					log(wrappedErr.Error())
					return wrappedErr
				}
				lines := strings.Split(string(out), "\n")
				if len(lines) == 0 {
					return fmt.Errorf("no pods found")
				}

				for i := 0; i < len(lines); i++ {
					line := strings.TrimSpace(lines[i])
					if len(line) > 0 && !strings.Contains(line, "Running") {
						wrappedErr := fmt.Errorf("pod is not in runnning state: '%s'", line)
						log(wrappedErr.Error())
						return wrappedErr
					}
				}
				return nil
			}, 5*time.Minute, 10*time.Second).Should(BeNil())
		})

		It("should create the Space CRD", func() {
			Eventually(func() error {
				out, err := execCommand("kubectl", "get", "crds", "--no-headers=true")
				if err != nil {
					wrappedErr := fmt.Errorf("failed to get crds: %s", string(out))
					log(wrappedErr.Error())
					return wrappedErr
				}

				for _, line := range strings.Split(string(out), "\n") {
					if strings.Contains(line, "spaces.decco.platform9.com") {
						return nil
					}
				}
				return fmt.Errorf("space CRD not found")

			}, 5*time.Minute, 10*time.Second).Should(BeNil())
		})
	})
})

func ensureCommandExists(name string) {
	By("Checking if prerequisite command exists: " + name)
	_, err := execCommand("which", name)
	Expect(err).To(BeNil(), "Could not find prerequisite command '%s'", name)
}

func stageManifest(manifestPath string) {
	By("Staging manifest: " + manifestPath)
	out, err := execCommand("cp", manifestPath, testStagingManifestsDir)
	Expect(err).To(BeNil(), "Failed to stage %s to %s: %s", manifestPath, testStagingManifestsDir, string(out))
}

func ensureDirExists(path string) {
	By("Checking if directory exists: " + path)
	inode, err := os.Stat(path)
	Expect(err).To(BeNil(), "Could not find directory '%s'", path)
	Expect(inode.IsDir()).To(BeTrue(), "File %s is not a directory", path)
}

func execCommand(name string, cmd ...string) ([]byte, error) {
	log("[exec] %s %s", name, strings.Join(cmd, " "))
	return exec.Command(name, cmd...).CombinedOutput()
}

func log(s string, args ...interface{}) {
	_, err := fmt.Fprintf(GinkgoWriter, s+"\n", args...)
	if err != nil {
		panic(err)
	}
}
