package fcvsock

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/fcapi"
)

const GuestAgentPort = 9100

func ShouldConfigure(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode == "" || mode == "vsock" || mode == "auto"
}

func GuestCID(vmID string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(vmID))
	return 3 + hash.Sum32()%60000
}

func Configure(socketPath, udsPath string, guestCID uint32) error {
	return fcapi.SendConfig(socketPath, "PUT", "/vsock", ConfigPayload(udsPath, guestCID))
}

func ConfigPayload(udsPath string, guestCID uint32) string {
	return fmt.Sprintf(`{"guest_cid":%d,"uds_path":%q}`, guestCID, udsPath)
}

func Dial(ctx context.Context, udsPath string, port int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", udsPath)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", port); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	} else {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !strings.HasPrefix(line, "OK ") {
		_ = conn.Close()
		return nil, fmt.Errorf("vsock connect rejected: %s", strings.TrimSpace(line))
	}
	return conn, nil
}
