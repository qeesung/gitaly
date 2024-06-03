package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	podID := getEnvOrExit("GITALY_POD_UID")
	cgroupPath := getEnvOrExit("CGROUP_PATH")
	outputPath := getEnvOrExit("OUTPUT_PATH")

	k8sPodID := strings.Replace(podID, "-", "_", -1) // eg. 2953d396_fd5e_4fc6_910f_ab687c9d08a8
	podCgroupPath := findPodCgroup(cgroupPath, k8sPodID)
	if podCgroupPath == "" {
		fmt.Printf("Error: failed to find cgroup for Gitaly pod %v\n", podID)
		os.Exit(1)
	}
	/* Minimal permissions that a pod will need in order to manage its own subtree.
	This is one way a cgroup can be delegated to a less privileged user, by granting write access
	of the directory and its "cgroup.procs", "cgroup.threads", "cgroup.subtree_control" files.
	Reference: https://docs.kernel.org/admin-guide/cgroup-v2.html#model-of-delegation
	*/
	modifyPaths := []string{"", "cgroup.procs", "cgroup.threads", "cgroup.subtree_control"}
	if err := changePermissions(podCgroupPath, modifyPaths); err != nil {
		fmt.Printf("Error: changing permissions for Gitaly pod %v cgroups: %v\n", podID, err)
		os.Exit(1)
	}

	if err := writePodCgroupPath(outputPath, podCgroupPath); err != nil {
		fmt.Printf("Error: failed to write pod cgroup path: %v\n", err)
		os.Exit(1)
	}
}

func getEnvOrExit(key string) string {
	value := os.Getenv(key)
	if value == "" {
		fmt.Printf("Error: %s environment variable is not set\n", key)
		os.Exit(1)
	}
	return value
}

// findPodCgroup finds the path to the pod's cgroup given the pod id.
func findPodCgroup(path string, uid string) string {
	directory := filepath.Join(path)
	files, _ := filepath.Glob(directory + "/*/*-pod" + uid + ".slice")

	if len(files) > 0 {
		return files[0]
	}
	return ""
}

// changePermissions changes permissions of cgroup paths enabling write access for the Gitaly pod
func changePermissions(podCgroupPath string, paths []string) error {
	for _, path := range paths {
		filePath := filepath.Join(podCgroupPath, path)
		if err := os.Chown(filePath, 1000, 1000); err != nil {
			return fmt.Errorf("chown cgroup path %q: %w", path, err)
		}
	}
	return nil
}

// writePodCgroupPath outputs the pod level cgroup path to a file
func writePodCgroupPath(outputPath string, podCgroupPath string) error {
	return os.WriteFile(outputPath, []byte(podCgroupPath), 0o644)
}
