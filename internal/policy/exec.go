package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// CommandExecutor runs an argv inside a container without invoking a shell.
type CommandExecutor interface {
	Exec(ctx context.Context, namespace, pod, container string, command []string) error
}

// SPDYCommandExecutor executes a command through the pods/exec subresource.
type SPDYCommandExecutor struct {
	config *rest.Config
	client kubernetes.Interface
}

// NewSPDYCommandExecutor returns an executor that uses the Kubernetes API.
func NewSPDYCommandExecutor(config *rest.Config, client kubernetes.Interface) *SPDYCommandExecutor {
	return &SPDYCommandExecutor{config: config, client: client}
}

// Exec runs command in the named container. The arguments are passed as argv.
func (e *SPDYCommandExecutor) Exec(ctx context.Context, namespace, pod, container string, command []string) error {
	if e == nil || e.config == nil || e.client == nil {
		return errors.New("command executor is not configured")
	}
	req := e.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: container,
		Command:   command,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(e.config, http.MethodPost, req.URL())
	if err != nil {
		return fmt.Errorf("create executor for pod %s: %w", pod, err)
	}
	var stdout, stderr bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return execError(namespace, pod, stderr.String(), err)
	}
	return nil
}

func execError(namespace, pod, stderr string, err error) error {
	if stderr != "" {
		return fmt.Errorf("exec %s/%s: %w (%s)", namespace, pod, err, stderr)
	}
	return fmt.Errorf("exec %s/%s: %w", namespace, pod, err)
}

func (e *ActionExecutor) execCommand(ctx context.Context, namespace string, params map[string]any) error {
	if e.exec == nil {
		return errors.New("command executor is not configured")
	}
	selector, err := stringParam(params, "podSelector")
	if err != nil {
		return err
	}
	command, err := stringSliceParam(params, "command")
	if err != nil {
		return err
	}
	requested := ""
	if _, ok := params["container"]; ok {
		requested, err = stringParam(params, "container")
		if err != nil {
			return err
		}
	}
	pod, err := e.runningPod(ctx, namespace, selector)
	if err != nil {
		return err
	}
	container, err := containerName(pod, requested)
	if err != nil {
		return err
	}
	if err := e.exec.Exec(ctx, namespace, pod.Name, container, command); err != nil {
		return fmt.Errorf("exec command: %w", err)
	}
	return nil
}

func (e *ActionExecutor) runningPod(ctx context.Context, namespace, selector string) (*corev1.Pod, error) {
	parsed, err := labels.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("podSelector: %w", err)
	}
	pods, err := e.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: parsed.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list pods in %s: %w", namespace, err)
	}
	running := runningPods(pods.Items)
	if len(running) == 0 {
		return nil, fmt.Errorf("no running pod in %s matched %q", namespace, selector)
	}
	sort.Slice(running, func(i, j int) bool {
		return running[i].Name < running[j].Name
	})
	return running[0], nil
}

func runningPods(pods []corev1.Pod) []*corev1.Pod {
	running := make([]*corev1.Pod, 0, len(pods))
	for i := range pods {
		if pods[i].Status.Phase == corev1.PodRunning {
			running = append(running, &pods[i])
		}
	}
	return running
}

func containerName(pod *corev1.Pod, requested string) (string, error) {
	if requested == "" {
		if len(pod.Spec.Containers) == 0 {
			return "", fmt.Errorf("pod %s has no containers", pod.Name)
		}
		return pod.Spec.Containers[0].Name, nil
	}
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == requested {
			return requested, nil
		}
	}
	return "", fmt.Errorf("pod %s has no container %s", pod.Name, requested)
}
