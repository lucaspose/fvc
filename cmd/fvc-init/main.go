package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const runtimeConfigPath = "/etc/fvc/runtime.json"
const exitCodeMarker = "FVC_EXIT_CODE="
const guestAgentPort = "9100"
const guestAgentVsockPort = 9100
const guestAgentTokenPrefix = "fvc_agent_token="
const guestAgentModePrefix = "fvc_agent_mode="

type RuntimeConfig struct {
	Env        []string `json:"env,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	RandomSeed string   `json:"random_seed,omitempty"`
}

type guestExecRequest struct {
	Command []string `json:"command"`
	Env     []string `json:"env,omitempty"`
	Workdir string   `json:"workdir,omitempty"`
}

type guestExecEvent struct {
	Stream       string `json:"stream,omitempty"`
	Data         []byte `json:"data,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type guestAgentServers struct {
	servers []*http.Server
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fvc-init: %v\n", err)
		fmt.Printf("%s%d\n", exitCodeMarker, 127)
		syncAndPoweroff()
	}
}

func run() error {
	ensureDefaultPath()
	mountBasics()
	config, err := loadRuntimeConfig(runtimeConfigPath)
	if err != nil {
		return err
	}
	if len(config.Cmd) == 0 {
		return fmt.Errorf("runtime command is empty")
	}
	if err := seedKernelRandom(config.RandomSeed); err != nil {
		fmt.Fprintf(os.Stderr, "fvc-init: random seed warning: %v\n", err)
	}
	agentToken := agentTokenFromCmdline()
	if agentToken != "" {
		servers, err := startGuestAgent(agentToken, agentModeFromCmdline())
		if err != nil {
			return err
		}
		defer shutdownGuestAgent(servers)
	}
	workdir := strings.TrimSpace(config.Workdir)
	if workdir == "" {
		workdir = "/"
	}
	if !filepath.IsAbs(workdir) {
		return fmt.Errorf("runtime workdir must be absolute: %s", workdir)
	}
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return fmt.Errorf("runtime workdir create failed: %w", err)
	}
	if err := os.Chdir(workdir); err != nil {
		return fmt.Errorf("runtime workdir change failed: %w", err)
	}

	env := append(os.Environ(), config.Env...)
	path, err := exec.LookPath(config.Cmd[0])
	if err != nil {
		return fmt.Errorf("runtime command lookup failed: %w", err)
	}
	cmd := exec.Command(path, config.Cmd[1:]...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("runtime command start failed: %w", err)
	}
	stopForward := forwardSignals(cmd.Process)
	err = cmd.Wait()
	stopForward()
	exitCode := commandExitCode(err)
	fmt.Printf("%s%d\n", exitCodeMarker, exitCode)
	syncAndPoweroff()
	return nil
}

func startGuestAgent(token, mode string) (*guestAgentServers, error) {
	mode = normalizeAgentMode(mode)
	mux := guestAgentMux(token)
	var servers guestAgentServers
	var failures []string
	if mode == "vsock" || mode == "auto" {
		if server, err := startGuestAgentVsock(mux); err == nil {
			servers.servers = append(servers.servers, server)
			fmt.Printf("FVC_AGENT_READY=vsock:%d\n", guestAgentVsockPort)
		} else {
			failures = append(failures, "vsock: "+err.Error())
		}
	}
	if mode == "tcp" || mode == "auto" {
		if server, err := startGuestAgentTCP(mux); err == nil {
			servers.servers = append(servers.servers, server)
			fmt.Printf("FVC_AGENT_READY=tcp:%s\n", guestAgentPort)
		} else {
			failures = append(failures, "tcp: "+err.Error())
		}
	}
	if len(servers.servers) == 0 {
		return nil, fmt.Errorf("guest agent listen failed in %s mode: %s", mode, strings.Join(failures, "; "))
	}
	return &servers, nil
}

func normalizeAgentMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tcp", "auto":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "vsock"
	}
}

const rndAddEntropy = 0x40085203

type randomPoolInfo struct {
	EntropyCount int32
	BufSize      int32
	Buf          [64]byte
}

func seedKernelRandom(encodedSeed string) error {
	encodedSeed = strings.TrimSpace(encodedSeed)
	if encodedSeed == "" {
		return nil
	}
	seed, err := base64.StdEncoding.DecodeString(encodedSeed)
	if err != nil {
		return fmt.Errorf("runtime random seed decode failed: %w", err)
	}
	defer zeroBytes(seed)
	if len(seed) == 0 {
		return fmt.Errorf("runtime random seed is empty")
	}
	if len(seed) > 64 {
		seed = seed[:64]
	}
	file, err := os.OpenFile("/dev/random", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open /dev/random failed: %w", err)
	}
	defer file.Close()
	var info randomPoolInfo
	info.EntropyCount = int32(len(seed) * 8)
	info.BufSize = int32(len(seed))
	copy(info.Buf[:], seed)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), uintptr(rndAddEntropy), uintptr(unsafe.Pointer(&info)))
	zeroBytes(info.Buf[:])
	if errno != 0 {
		return fmt.Errorf("RNDADDENTROPY failed: %w", errno)
	}
	return nil
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func guestAgentMux(token string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		handleGuestExec(w, r, token)
	})
	return mux
}

func startGuestAgentTCP(handler http.Handler) (*http.Server, error) {
	server := &http.Server{
		Addr:              ":" + guestAgentPort,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, err
	}
	serveGuestAgent(server, listener)
	return server, nil
}

func startGuestAgentVsock(handler http.Handler) (*http.Server, error) {
	listener, err := listenVsock(guestAgentVsockPort)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	serveGuestAgent(server, listener)
	return server, nil
}

func serveGuestAgent(server *http.Server, listener net.Listener) {
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "fvc-init: guest agent stopped: %v\n", err)
		}
	}()
}

func listenVsock(port uint32) (net.Listener, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := unix.Listen(fd, 32); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &vsockListener{fd: fd, addr: vsockAddr{cid: unix.VMADDR_CID_ANY, port: port}}, nil
}

type vsockAddr struct {
	cid  uint32
	port uint32
}

func (a vsockAddr) Network() string {
	return "vsock"
}

func (a vsockAddr) String() string {
	return fmt.Sprintf("%d:%d", a.cid, a.port)
}

type vsockListener struct {
	fd   int
	addr vsockAddr
	mu   sync.Mutex
	done bool
}

func (l *vsockListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	closed := l.done
	l.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	fd, sa, err := unix.Accept4(l.fd, unix.SOCK_CLOEXEC)
	if err != nil {
		l.mu.Lock()
		closed = l.done
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		return nil, err
	}
	remote := vsockAddr{}
	if vm, ok := sa.(*unix.SockaddrVM); ok {
		remote = vsockAddr{cid: vm.CID, port: vm.Port}
	}
	return &vsockConn{fd: fd, local: l.addr, remote: remote}, nil
}

func (l *vsockListener) Close() error {
	l.mu.Lock()
	if l.done {
		l.mu.Unlock()
		return nil
	}
	l.done = true
	fd := l.fd
	l.mu.Unlock()
	return unix.Close(fd)
}

func (l *vsockListener) Addr() net.Addr {
	return l.addr
}

type vsockConn struct {
	fd     int
	local  vsockAddr
	remote vsockAddr
}

func (c *vsockConn) Read(b []byte) (int, error) {
	return unix.Read(c.fd, b)
}

func (c *vsockConn) Write(b []byte) (int, error) {
	return unix.Write(c.fd, b)
}

func (c *vsockConn) Close() error {
	return unix.Close(c.fd)
}

func (c *vsockConn) LocalAddr() net.Addr {
	return c.local
}

func (c *vsockConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *vsockConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *vsockConn) SetReadDeadline(t time.Time) error {
	return setSocketTimeout(c.fd, unix.SO_RCVTIMEO, t)
}

func (c *vsockConn) SetWriteDeadline(t time.Time) error {
	return setSocketTimeout(c.fd, unix.SO_SNDTIMEO, t)
}

func setSocketTimeout(fd int, opt int, deadline time.Time) error {
	var tv unix.Timeval
	if !deadline.IsZero() {
		duration := time.Until(deadline)
		if duration <= 0 {
			duration = time.Nanosecond
		}
		tv = unix.NsecToTimeval(duration.Nanoseconds())
	}
	return unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, opt, &tv)
}

func shutdownGuestAgent(servers *guestAgentServers) {
	if servers == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, server := range servers.servers {
		_ = server.Shutdown(ctx)
	}
}

func handleGuestExec(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorized(r, token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()
	var req guestExecRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid exec request", http.StatusBadRequest)
		return
	}
	if err := validateGuestExecRequest(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	encoder := json.NewEncoder(w)
	var sendMu sync.Mutex
	send := func(event guestExecEvent) {
		sendMu.Lock()
		defer sendMu.Unlock()
		_ = encoder.Encode(event)
		if flusher != nil {
			flusher.Flush()
		}
	}

	path, err := exec.LookPath(req.Command[0])
	if err != nil {
		send(guestExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: err.Error()})
		return
	}
	cmd := exec.CommandContext(r.Context(), path, req.Command[1:]...)
	cmd.Env = append(os.Environ(), req.Env...)
	if strings.TrimSpace(req.Workdir) != "" {
		cmd.Dir = req.Workdir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		send(guestExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		send(guestExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		send(guestExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: err.Error()})
		return
	}
	done := make(chan struct{}, 2)
	go streamCommandOutput(stdout, "stdout", send, done)
	go streamCommandOutput(stderr, "stderr", send, done)
	err = cmd.Wait()
	<-done
	<-done
	exitCode := commandExitCode(err)
	if r.Context().Err() != nil {
		exitCode = 130
	}
	send(guestExecEvent{Stream: "exit", ExitCode: exitCode})
}

func streamCommandOutput(reader io.Reader, stream string, send func(guestExecEvent), done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buf := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			send(guestExecEvent{Stream: stream, Data: chunk})
		}
		if err != nil {
			return
		}
	}
}

func validateGuestExecRequest(req guestExecRequest) error {
	if len(req.Command) == 0 {
		return fmt.Errorf("exec command is required")
	}
	for _, arg := range req.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("exec command contains an empty argument")
		}
	}
	for _, env := range req.Env {
		if !strings.Contains(env, "=") || strings.HasPrefix(env, "=") || strings.ContainsAny(env, "\x00\r\n") {
			return fmt.Errorf("invalid exec env entry: %q", env)
		}
	}
	if strings.TrimSpace(req.Workdir) != "" && !filepath.IsAbs(req.Workdir) {
		return fmt.Errorf("exec workdir must be absolute")
	}
	return nil
}

func authorized(r *http.Request, token string) bool {
	return token != "" && r.Header.Get("Authorization") == "Bearer "+token
}

func agentTokenFromCmdline() string {
	return cmdlineValue(guestAgentTokenPrefix)
}

func agentModeFromCmdline() string {
	return cmdlineValue(guestAgentModePrefix)
}

func cmdlineValue(prefix string) string {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Split(bufio.ScanWords)
	for scanner.Scan() {
		arg := scanner.Text()
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
		}
	}
	return ""
}

func forwardSignals(process *os.Process) func() {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				if process != nil {
					_ = process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
	}
	return 127
}

func syncAndPoweroff() {
	syscall.Sync()
	for {
		_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
		select {}
	}
}

func ensureDefaultPath() {
	if strings.TrimSpace(os.Getenv("PATH")) != "" {
		return
	}
	_ = os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
}

func mountBasics() {
	_ = os.MkdirAll("/proc", 0555)
	_ = os.MkdirAll("/sys", 0555)
	_ = os.MkdirAll("/dev", 0755)
	_ = syscall.Mount("proc", "/proc", "proc", 0, "")
	_ = syscall.Mount("sysfs", "/sys", "sysfs", 0, "")
	_ = syscall.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")
	_ = exec.Command("ip", "link", "set", "lo", "up").Run()
	configureNetworkFromCmdline()
}

func configureNetworkFromCmdline() {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return
	}
	for _, arg := range strings.Fields(string(data)) {
		if strings.HasPrefix(arg, "ip=") {
			applyKernelIP(strings.TrimPrefix(arg, "ip="))
			return
		}
	}
}

func applyKernelIP(value string) {
	parts := strings.Split(value, ":")
	if len(parts) < 6 {
		return
	}
	guestIP, hostIP, netmask, iface := parts[0], parts[2], parts[3], parts[5]
	if iface == "" {
		iface = "eth0"
	}
	_ = exec.Command("ip", "link", "set", iface, "up").Run()
	if guestIP != "" && netmask != "" {
		_ = exec.Command("ip", "addr", "add", guestIP+"/"+netmask, "dev", iface).Run()
	}
	if hostIP != "" {
		_ = exec.Command("ip", "route", "add", "default", "via", hostIP, "dev", iface).Run()
	}
}

func loadRuntimeConfig(path string) (RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("runtime config read failed: %w", err)
	}
	var config RuntimeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return RuntimeConfig{}, fmt.Errorf("runtime config parse failed: %w", err)
	}
	for _, env := range config.Env {
		if !strings.Contains(env, "=") || strings.HasPrefix(env, "=") {
			return RuntimeConfig{}, fmt.Errorf("invalid runtime env entry: %q", env)
		}
	}
	for _, arg := range config.Cmd {
		if strings.TrimSpace(arg) == "" {
			return RuntimeConfig{}, fmt.Errorf("runtime command contains an empty argument")
		}
	}
	return config, nil
}
