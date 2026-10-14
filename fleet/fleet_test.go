package fleet

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// TestDescribeWorkerError pins the user-facing wording: raw Go/net error
// strings must not leak into the fleet view.
func TestDescribeWorkerError(t *testing.T) {
	refused := &url.Error{
		Op:  "Get",
		URL: "http://titan:8080/api/v1/models",
		Err: &net.OpError{Op: "dial", Net: "tcp",
			Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)},
	}
	unreachable := &url.Error{
		Op:  "Get",
		URL: "http://titan:8080/api/v1/models",
		Err: &net.OpError{Op: "dial", Net: "tcp",
			Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)},
	}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unauthorized", &WorkerError{StatusCode: 401, Message: "api-key header is required"},
			"Unauthorized — check the api-key"},
		{"worker message", &WorkerError{StatusCode: 404, Message: "model 'x' not found"},
			"model 'x' not found"},
		{"timeout", &url.Error{Op: "Get", URL: "u", Err: context.DeadlineExceeded},
			"Timed out"},
		{"connection refused", refused, "Connection refused — the manager isn't running"},
		{"host unreachable", unreachable, "Network unreachable"},
		{"dns", &net.DNSError{Err: "no such host", Name: "titan", IsNotFound: true},
			"Host name could not be resolved"},
		{"generic", errors.New("some raw failure"), "Unreachable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeWorkerError(tc.err); got != tc.want {
				t.Fatalf("describeWorkerError() = %q, want %q", got, tc.want)
			}
		})
	}
}
