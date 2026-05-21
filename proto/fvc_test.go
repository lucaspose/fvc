package proto

import "testing"

func TestFvcServiceDescriptor(t *testing.T) {
	if FvcService_ServiceDesc.ServiceName != "fvc.FvcService" {
		t.Fatalf("unexpected service name: %s", FvcService_ServiceDesc.ServiceName)
	}
	if len(FvcService_ServiceDesc.Methods) != 26 {
		t.Fatalf("expected 26 unary methods, got %d", len(FvcService_ServiceDesc.Methods))
	}
	if len(FvcService_ServiceDesc.Streams) != 5 {
		t.Fatalf("expected 5 stream methods, got %d", len(FvcService_ServiceDesc.Streams))
	}

	methods := map[string]bool{}
	for _, method := range FvcService_ServiceDesc.Methods {
		methods[method.MethodName] = true
	}
	for _, name := range []string{"Run", "Stop", "Start", "Rm", "ConsoleInfo", "Inspect", "PullImage", "ListImages", "Prune", "Stats", "Wait", "Kill", "SnapshotCreate", "SnapshotList", "SnapshotRestore", "SnapshotRemove", "BuildImage", "ImageInspect", "ImageRemove", "ImageTag", "ImageImport", "ImageExport", "ImageHistory", "ImagePrune", "Ps", "Diagnostics"} {
		if !methods[name] {
			t.Fatalf("missing unary method %s", name)
		}
	}
	streams := map[string]bool{}
	for _, stream := range FvcService_ServiceDesc.Streams {
		streams[stream.StreamName] = stream.ServerStreams
	}
	for _, name := range []string{"RunStream", "Exec", "PullImageStream", "BuildImageStream", "StreamLogs"} {
		if !streams[name] {
			t.Fatalf("%s should be a server stream", name)
		}
	}
}

func TestGeneratedMessageGetters(t *testing.T) {
	req := &RunRequest{
		Source: "ubuntu",
		Config: &VmConfig{
			Cpus:     2,
			MemoryMb: 1024,
			Ports:    []string{"8080:80"},
		},
	}

	if req.GetSource() != "ubuntu" {
		t.Fatalf("unexpected source: %s", req.GetSource())
	}
	if req.GetConfig().GetCpus() != 2 {
		t.Fatalf("unexpected cpu count: %d", req.GetConfig().GetCpus())
	}
	if req.GetConfig().GetMemoryMb() != 1024 {
		t.Fatalf("unexpected memory: %d", req.GetConfig().GetMemoryMb())
	}
	if got := req.GetConfig().GetPorts(); len(got) != 1 || got[0] != "8080:80" {
		t.Fatalf("unexpected ports: %v", got)
	}

	details := &VmDetails{
		VmId:        "vm-1",
		GuestIp:     "172.16.0.2",
		MacAddress:  "02:FC:00:00:00:01",
		TapName:     "fvc123",
		LogPath:     "/tmp/vm.log",
		DrivePath:   "/tmp/vm.ext4",
		ConsolePath: "/tmp/vm.in",
	}
	if details.GetGuestIp() != "172.16.0.2" {
		t.Fatalf("unexpected guest ip: %s", details.GetGuestIp())
	}
	if details.GetMacAddress() != "02:FC:00:00:00:01" {
		t.Fatalf("unexpected mac: %s", details.GetMacAddress())
	}
	if details.GetTapName() != "fvc123" {
		t.Fatalf("unexpected tap name: %s", details.GetTapName())
	}
	if details.GetLogPath() != "/tmp/vm.log" {
		t.Fatalf("unexpected log path: %s", details.GetLogPath())
	}

	image := &ImageDetails{
		Image:        "ubuntu",
		Path:         "/cache/ubuntu.ext4",
		SizeBytes:    1024,
		Env:          []string{"PORT=80"},
		Cmd:          []string{"/bin/server"},
		Workdir:      "/srv",
		ExposedPorts: []int32{80},
	}
	if image.GetImage() != "ubuntu" || image.GetSizeBytes() != 1024 {
		t.Fatalf("unexpected image details: %#v", image)
	}
	if image.GetWorkdir() != "/srv" || image.GetEnv()[0] != "PORT=80" || image.GetCmd()[0] != "/bin/server" || image.GetExposedPorts()[0] != 80 {
		t.Fatalf("unexpected image runtime metadata: %#v", image)
	}
}
