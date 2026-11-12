package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Proxy connects in and out to the ssh port of the pod of the server through the API of the cluster, until one of
// them ends. It is the ProxyCommand of the ssh config (viper k8s-proxy): nothing but the data of the connection may
// go to out.
func (c *Client) Proxy(ctx context.Context, server string, in io.Reader, out io.Writer) error {
	pod, err := c.readyPod(ctx, server)
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	port, failed, err := c.forward(ctx, pod, stop)
	if err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, in)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(out, conn); done <- struct{}{} }()
	select {
	case <-done:
	case err := <-failed:
		return fmt.Errorf("port forward to %s ended: %w", pod.Name, err)
	case <-ctx.Done():
	}
	return nil
}

// readyPod returns the pod of the server if it can be connected to.
func (c *Client) readyPod(ctx context.Context, server string) (*corev1.Pod, error) {
	if _, err := c.client(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()
	if cfg == nil {
		return nil, errors.New("no connection to the API of a cluster (a client made for tests)")
	}
	pod, err := c.podOf(ctx, server)
	if err != nil {
		return nil, err
	}
	if pod == nil {
		return nil, fmt.Errorf("the pod %s does not exist in %s (is the machine running? viper up)", podName(server), c.opts.namespace())
	}
	if !podReady(pod) {
		return nil, fmt.Errorf("the pod %s is not ready (%s)", pod.Name, pod.Status.Phase)
	}
	return pod, nil
}

// forward starts the port forward to the ssh port of the pod and returns the local port; failed reports its end.
func (c *Client) forward(ctx context.Context, pod *corev1.Pod, stop chan struct{}) (port uint16, failed chan error, err error) {
	cs, err := c.client()
	if err != nil {
		return 0, nil, err
	}
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()
	rt, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return 0, nil, err
	}
	u := cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(c.opts.namespace()).Name(pod.Name).SubResource("portforward").URL()
	ready := make(chan struct{})
	fw, err := portforward.NewOnAddresses(spdy.NewDialer(upgrader, &http.Client{Transport: rt}, http.MethodPost, u),
		[]string{"127.0.0.1"}, []string{fmt.Sprintf("0:%d", SSHPort)}, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return 0, nil, err
	}
	failed = make(chan error, 1)
	go func() { failed <- fw.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-failed:
		return 0, nil, fmt.Errorf("port forward to %s: %w", pod.Name, err)
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		return 0, nil, fmt.Errorf("port forward to %s: no local port", pod.Name)
	}
	return ports[0].Local, failed, nil
}
