package main

import (
	"context"
	"errors"
	"io"

	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeFvcClient struct {
	runReq         *proto.RunRequest
	runEvents      []*proto.RunEvent
	buildReq       *proto.BuildImageRequest
	buildEvents    []*proto.OperationEvent
	pullReq        *proto.PullImageRequest
	pullEvents     []*proto.OperationEvent
	stopReq        *proto.StopRequest
	startReq       *proto.StartRequest
	rmReq          *proto.RmRequest
	renameReq      *proto.RenameRequest
	updateReq      *proto.UpdateRequest
	psReq          *proto.PsRequest
	inspectReq     *proto.InspectRequest
	statsReq       *proto.StatsRequest
	waitReq        *proto.WaitRequest
	killReq        *proto.KillRequest
	pruneReq       *proto.PruneRequest
	listImagesReq  *proto.ListImagesRequest
	logsReq        *proto.LogsRequest
	logsEvents     []*proto.LogsResponse
	execReq        *proto.ExecRequest
	execEvents     []*proto.ExecEvent
	snapshotCreate *proto.SnapshotCreateRequest
	imageRemove    *proto.ImageRemoveRequest
	imageImport    *proto.ImageImportRequest
	imageExport    *proto.ImageExportRequest
	volumeCreate   *proto.VolumeCreateRequest
	volumeRemove   *proto.VolumeRemoveRequest
	volumePrune    *proto.VolumePruneRequest
}

func (f *fakeFvcClient) Run(context.Context, *proto.RunRequest, ...grpc.CallOption) (*proto.RunResponse, error) {
	return nil, errors.New("unexpected Run call")
}

func (f *fakeFvcClient) RunStream(_ context.Context, req *proto.RunRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[proto.RunEvent], error) {
	f.runReq = req
	return newFakeStream(f.runEvents), nil
}

func (f *fakeFvcClient) Stop(_ context.Context, req *proto.StopRequest, _ ...grpc.CallOption) (*proto.StopResponse, error) {
	f.stopReq = req
	return &proto.StopResponse{Success: true, Message: "microVM stopped"}, nil
}

func (f *fakeFvcClient) Start(_ context.Context, req *proto.StartRequest, _ ...grpc.CallOption) (*proto.StartResponse, error) {
	f.startReq = req
	return &proto.StartResponse{Success: true, Message: "microVM started"}, nil
}

func (f *fakeFvcClient) Rm(_ context.Context, req *proto.RmRequest, _ ...grpc.CallOption) (*proto.RmResponse, error) {
	f.rmReq = req
	return &proto.RmResponse{Success: true, Message: "microVM removed"}, nil
}

func (f *fakeFvcClient) Rename(_ context.Context, req *proto.RenameRequest, _ ...grpc.CallOption) (*proto.RenameResponse, error) {
	f.renameReq = req
	return &proto.RenameResponse{Success: true, Message: "microVM renamed"}, nil
}

func (f *fakeFvcClient) Update(_ context.Context, req *proto.UpdateRequest, _ ...grpc.CallOption) (*proto.UpdateResponse, error) {
	f.updateReq = req
	return &proto.UpdateResponse{Success: true, Message: "microVM updated"}, nil
}

func (f *fakeFvcClient) ConsoleInfo(context.Context, *proto.ConsoleInfoRequest, ...grpc.CallOption) (*proto.ConsoleInfoResponse, error) {
	return nil, errors.New("unexpected ConsoleInfo call")
}

func (f *fakeFvcClient) Exec(_ context.Context, req *proto.ExecRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[proto.ExecEvent], error) {
	f.execReq = req
	return newFakeStream(f.execEvents), nil
}

func (f *fakeFvcClient) Inspect(_ context.Context, req *proto.InspectRequest, _ ...grpc.CallOption) (*proto.InspectResponse, error) {
	f.inspectReq = req
	return &proto.InspectResponse{Success: true, Vm: &proto.VmDetails{VmId: req.VmId, Status: "running"}}, nil
}

func (f *fakeFvcClient) PullImage(context.Context, *proto.PullImageRequest, ...grpc.CallOption) (*proto.PullImageResponse, error) {
	return nil, errors.New("unexpected PullImage call")
}

func (f *fakeFvcClient) PullImageStream(_ context.Context, req *proto.PullImageRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[proto.OperationEvent], error) {
	f.pullReq = req
	return newFakeStream(f.pullEvents), nil
}

func (f *fakeFvcClient) ListImages(_ context.Context, req *proto.ListImagesRequest, _ ...grpc.CallOption) (*proto.ListImagesResponse, error) {
	f.listImagesReq = req
	return &proto.ListImagesResponse{Images: []*proto.ImageDetails{{Image: "ubuntu", SizeBytes: 1024, Path: "/cache/ubuntu.ext4"}}}, nil
}

func (f *fakeFvcClient) Prune(_ context.Context, req *proto.PruneRequest, _ ...grpc.CallOption) (*proto.PruneResponse, error) {
	f.pruneReq = req
	return &proto.PruneResponse{Success: true, Message: "prune complete", RemovedFiles: 1, FreedBytes: 1024}, nil
}

func (f *fakeFvcClient) Stats(_ context.Context, req *proto.StatsRequest, _ ...grpc.CallOption) (*proto.StatsResponse, error) {
	f.statsReq = req
	return &proto.StatsResponse{Stats: []*proto.VmStats{{VmId: req.VmId, Status: "running", MemoryMb: 512}}}, nil
}

func (f *fakeFvcClient) Wait(_ context.Context, req *proto.WaitRequest, _ ...grpc.CallOption) (*proto.WaitResponse, error) {
	f.waitReq = req
	return &proto.WaitResponse{Success: true, Message: "vm stopped", Status: "stopped", ExitCode: 0}, nil
}

func (f *fakeFvcClient) Kill(_ context.Context, req *proto.KillRequest, _ ...grpc.CallOption) (*proto.KillResponse, error) {
	f.killReq = req
	return &proto.KillResponse{Success: true, Message: "microVM killed"}, nil
}

func (f *fakeFvcClient) SnapshotCreate(_ context.Context, req *proto.SnapshotCreateRequest, _ ...grpc.CallOption) (*proto.SnapshotCreateResponse, error) {
	f.snapshotCreate = req
	return &proto.SnapshotCreateResponse{Success: true, Message: "snapshot created", Snapshot: &proto.SnapshotDetails{Name: req.Name, Path: "/snap", SizeBytes: 12}}, nil
}

func (f *fakeFvcClient) SnapshotList(context.Context, *proto.SnapshotListRequest, ...grpc.CallOption) (*proto.SnapshotListResponse, error) {
	return nil, errors.New("unexpected SnapshotList call")
}

func (f *fakeFvcClient) SnapshotRestore(context.Context, *proto.SnapshotRestoreRequest, ...grpc.CallOption) (*proto.SnapshotRestoreResponse, error) {
	return nil, errors.New("unexpected SnapshotRestore call")
}

func (f *fakeFvcClient) SnapshotRemove(context.Context, *proto.SnapshotRemoveRequest, ...grpc.CallOption) (*proto.SnapshotRemoveResponse, error) {
	return nil, errors.New("unexpected SnapshotRemove call")
}

func (f *fakeFvcClient) BuildImage(context.Context, *proto.BuildImageRequest, ...grpc.CallOption) (*proto.BuildImageResponse, error) {
	return nil, errors.New("unexpected BuildImage call")
}

func (f *fakeFvcClient) BuildImageStream(_ context.Context, req *proto.BuildImageRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[proto.OperationEvent], error) {
	f.buildReq = req
	return newFakeStream(f.buildEvents), nil
}

func (f *fakeFvcClient) ImageInspect(context.Context, *proto.ImageInspectRequest, ...grpc.CallOption) (*proto.ImageInspectResponse, error) {
	return nil, errors.New("unexpected ImageInspect call")
}

func (f *fakeFvcClient) ImageRemove(_ context.Context, req *proto.ImageRemoveRequest, _ ...grpc.CallOption) (*proto.ImageRemoveResponse, error) {
	f.imageRemove = req
	return &proto.ImageRemoveResponse{Success: true, Message: "image removed"}, nil
}

func (f *fakeFvcClient) ImageTag(context.Context, *proto.ImageTagRequest, ...grpc.CallOption) (*proto.ImageTagResponse, error) {
	return nil, errors.New("unexpected ImageTag call")
}

func (f *fakeFvcClient) ImageImport(_ context.Context, req *proto.ImageImportRequest, _ ...grpc.CallOption) (*proto.ImageImportResponse, error) {
	f.imageImport = req
	return &proto.ImageImportResponse{Success: true, Message: "image imported", Image: &proto.ImageDetails{Image: req.Image, Path: "/cache/rootfs.ext4"}}, nil
}

func (f *fakeFvcClient) ImageExport(_ context.Context, req *proto.ImageExportRequest, _ ...grpc.CallOption) (*proto.ImageExportResponse, error) {
	f.imageExport = req
	return &proto.ImageExportResponse{Success: true, Message: "image exported", Path: req.DestPath}, nil
}

func (f *fakeFvcClient) ImageHistory(context.Context, *proto.ImageHistoryRequest, ...grpc.CallOption) (*proto.ImageHistoryResponse, error) {
	return nil, errors.New("unexpected ImageHistory call")
}

func (f *fakeFvcClient) ImagePrune(context.Context, *proto.ImagePruneRequest, ...grpc.CallOption) (*proto.ImagePruneResponse, error) {
	return nil, errors.New("unexpected ImagePrune call")
}

func (f *fakeFvcClient) VolumeCreate(_ context.Context, req *proto.VolumeCreateRequest, _ ...grpc.CallOption) (*proto.VolumeCreateResponse, error) {
	f.volumeCreate = req
	return &proto.VolumeCreateResponse{Success: true, Message: "volume created", Volume: &proto.VolumeDetails{Name: req.Name, SizeBytes: req.SizeMb * 1024 * 1024, Path: "/volumes/" + req.Name + ".ext4"}}, nil
}

func (f *fakeFvcClient) VolumeList(context.Context, *proto.VolumeListRequest, ...grpc.CallOption) (*proto.VolumeListResponse, error) {
	return &proto.VolumeListResponse{Success: true, Message: "volumes listed", Volumes: []*proto.VolumeDetails{{Name: "data", SizeBytes: 128 * 1024 * 1024, Path: "/volumes/data.ext4"}}}, nil
}

func (f *fakeFvcClient) VolumeInspect(context.Context, *proto.VolumeInspectRequest, ...grpc.CallOption) (*proto.VolumeInspectResponse, error) {
	return nil, errors.New("unexpected VolumeInspect call")
}

func (f *fakeFvcClient) VolumeRemove(_ context.Context, req *proto.VolumeRemoveRequest, _ ...grpc.CallOption) (*proto.VolumeRemoveResponse, error) {
	f.volumeRemove = req
	return &proto.VolumeRemoveResponse{Success: true, Message: "volume removed", Volume: &proto.VolumeDetails{Name: req.Name, SizeBytes: 128 * 1024 * 1024, Path: "/volumes/" + req.Name + ".ext4"}}, nil
}

func (f *fakeFvcClient) VolumePrune(_ context.Context, req *proto.VolumePruneRequest, _ ...grpc.CallOption) (*proto.VolumePruneResponse, error) {
	f.volumePrune = req
	return &proto.VolumePruneResponse{Success: true, Message: "volume prune complete", RemovedVolumes: 1, FreedBytes: 128 * 1024 * 1024}, nil
}

func (f *fakeFvcClient) Ps(_ context.Context, req *proto.PsRequest, _ ...grpc.CallOption) (*proto.PsResponse, error) {
	f.psReq = req
	return &proto.PsResponse{Vms: []*proto.VmDetails{{VmId: "vm-1", Name: "web", Status: "running", Image: "ubuntu", Config: &proto.VmConfig{Cpus: 1, MemoryMb: 512}}}}, nil
}

func (f *fakeFvcClient) StreamLogs(_ context.Context, req *proto.LogsRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[proto.LogsResponse], error) {
	f.logsReq = req
	return newFakeStream(f.logsEvents), nil
}

func (f *fakeFvcClient) Diagnostics(context.Context, *proto.DiagnosticsRequest, ...grpc.CallOption) (*proto.DiagnosticsResponse, error) {
	return nil, errors.New("unexpected Diagnostics call")
}

type fakeStream[T any] struct {
	events []*T
	index  int
}

func newFakeStream[T any](events []*T) *fakeStream[T] {
	return &fakeStream[T]{events: events}
}

func (s *fakeStream[T]) Recv() (*T, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *fakeStream[T]) Header() (metadata.MD, error) {
	return nil, nil
}

func (s *fakeStream[T]) Trailer() metadata.MD {
	return nil
}

func (s *fakeStream[T]) CloseSend() error {
	return nil
}

func (s *fakeStream[T]) Context() context.Context {
	return context.Background()
}

func (s *fakeStream[T]) SendMsg(any) error {
	return nil
}

func (s *fakeStream[T]) RecvMsg(any) error {
	return nil
}
