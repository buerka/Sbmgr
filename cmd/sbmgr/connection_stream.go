package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ConnectionSample is a cumulative, application-level reading for one routed
// connection. ID includes sing-box's creation time to survive its restarts.
type ConnectionSample struct {
	ID, AuthUser, Domain, Destination string
	StartedAt, ClosedAt               time.Time
	Upload, Download                  int64
}

const (
	connectionStreamLimit     = 65536
	connectionStreamRecvLimit = 64 << 20
	connectionStreamInterval  = 5 * time.Second
	connectionStatusInterval  = 10 * time.Second
)

// These messages follow daemon/started_service.proto in sing-box v1.14.1.
// The existing grpc/protobuf dependencies adapt the legacy message interface.
type streamSubscribeRequest struct {
	Interval int64 `protobuf:"varint,1,opt,name=interval,proto3"`
}

func (*streamSubscribeRequest) Reset()         {}
func (*streamSubscribeRequest) String() string { return "connection subscription" }
func (*streamSubscribeRequest) ProtoMessage()  {}

type streamEvents struct {
	Events []*streamEvent `protobuf:"bytes,1,rep,name=events,proto3"`
	Reset_ bool           `protobuf:"varint,2,opt,name=reset,proto3"`
}

func (m *streamEvents) Reset()       { *m = streamEvents{} }
func (*streamEvents) String() string { return "connection events" }
func (*streamEvents) ProtoMessage()  {}

type streamEvent struct {
	Type          int32             `protobuf:"varint,1,opt,name=type,proto3"`
	ID            string            `protobuf:"bytes,2,opt,name=id,proto3"`
	Connection    *streamConnection `protobuf:"bytes,3,opt,name=connection,proto3"`
	UplinkDelta   int64             `protobuf:"varint,4,opt,name=uplinkDelta,proto3"`
	DownlinkDelta int64             `protobuf:"varint,5,opt,name=downlinkDelta,proto3"`
	ClosedAt      int64             `protobuf:"varint,6,opt,name=closedAt,proto3"`
}

func (m *streamEvent) Reset()       { *m = streamEvent{} }
func (*streamEvent) String() string { return "connection event" }
func (*streamEvent) ProtoMessage()  {}

type streamConnection struct {
	ID            string `protobuf:"bytes,1,opt,name=id,proto3"`
	Inbound       string `protobuf:"bytes,2,opt,name=inbound,proto3"`
	InboundType   string `protobuf:"bytes,3,opt,name=inboundType,proto3"`
	Network       string `protobuf:"bytes,5,opt,name=network,proto3"`
	Destination   string `protobuf:"bytes,7,opt,name=destination,proto3"`
	Domain        string `protobuf:"bytes,8,opt,name=domain,proto3"`
	AuthUser      string `protobuf:"bytes,10,opt,name=user,proto3"`
	CreatedAt     int64  `protobuf:"varint,12,opt,name=createdAt,proto3"`
	ClosedAt      int64  `protobuf:"varint,13,opt,name=closedAt,proto3"`
	UplinkTotal   int64  `protobuf:"varint,16,opt,name=uplinkTotal,proto3"`
	DownlinkTotal int64  `protobuf:"varint,17,opt,name=downlinkTotal,proto3"`
}

func (m *streamConnection) Reset()       { *m = streamConnection{} }
func (*streamConnection) String() string { return "connection" }
func (*streamConnection) ProtoMessage()  {}

type streamTracked struct {
	sample ConnectionSample
	valid  bool
}

// watchConnectionStream follows one sing-box API stream. Its caller reconnects
// after errors; every new subscription begins with a reset snapshot containing
// active and up to 1000 recently closed connections. No remote address or weak
// unauthenticated endpoint is accepted here.
func watchConnectionStream(ctx context.Context, address, secret string, onBatch func([]ConnectionSample, bool) error) error {
	if !connectionStreamAddressOK(address) || len(secret) < 32 || onBatch == nil {
		return errors.New("连接统计流配置无效")
	}
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(connectionStreamRecvLimit)))
	if err != nil {
		return errors.New("连接统计流不可用")
	}
	defer conn.Close()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	authCtx := metadata.AppendToOutgoingContext(streamCtx, "authorization", "Bearer "+secret)
	stream, err := conn.NewStream(authCtx, &grpc.StreamDesc{ServerStreams: true}, "/daemon.StartedService/SubscribeConnections")
	if err != nil {
		return errors.New("连接统计流不可用")
	}
	if err := stream.SendMsg(&streamSubscribeRequest{Interval: int64(connectionStreamInterval)}); err != nil {
		return errors.New("连接统计流不可用")
	}
	if err := stream.CloseSend(); err != nil {
		return errors.New("连接统计流不可用")
	}
	// Unlike SubscribeConnections, SubscribeStatus sends a response on every
	// interval even while idle. A local ticker cannot establish source liveness.
	statusStream, err := conn.NewStream(authCtx, &grpc.StreamDesc{ServerStreams: true}, "/daemon.StartedService/SubscribeStatus")
	if err != nil {
		return errors.New("连接统计流不可用")
	}
	if err := statusStream.SendMsg(&streamSubscribeRequest{Interval: int64(connectionStatusInterval)}); err != nil {
		return errors.New("连接统计流不可用")
	}
	if err := statusStream.CloseSend(); err != nil {
		return errors.New("连接统计流不可用")
	}
	tracked := make(map[string]streamTracked)
	type received struct {
		message streamEvents
		err     error
	}
	receivedEvents := make(chan received, 1)
	statusEvents := make(chan error, 1)
	go func() {
		for {
			var message streamEvents
			err := stream.RecvMsg(&message)
			select {
			case receivedEvents <- received{message, err}:
			case <-streamCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		for {
			err := statusStream.RecvMsg(&emptypb.Empty{})
			select {
			case statusEvents <- err:
			case <-streamCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	seenReset := false
	var lastPublished time.Time
	publish := func(samples []ConnectionSample, reset bool) error {
		// Traffic batches already refresh coverage. A nearby status heartbeat
		// should not acquire the state lock and write the same metadata again.
		if !reset && len(samples) == 0 && !lastPublished.IsZero() && time.Since(lastPublished) < connectionStatusInterval {
			return nil
		}
		if err := onBatch(samples, reset); err != nil {
			return err
		}
		lastPublished = time.Now()
		return nil
	}
	statusDeadline := time.NewTimer(3 * connectionStatusInterval)
	defer statusDeadline.Stop()
	for {
		var result received
		select {
		case result = <-receivedEvents:
		case err := <-statusEvents:
			if err != nil {
				return errors.New("连接统计流中断")
			}
			statusDeadline.Reset(3 * connectionStatusInterval)
			if seenReset {
				if err := publish(nil, false); err != nil {
					return err
				}
			}
			continue
		case <-statusDeadline.C:
			return errors.New("连接统计流中断")
		case <-ctx.Done():
			return ctx.Err()
		}
		if result.err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("连接统计流中断")
		}
		message := result.message
		if message.Reset_ {
			seenReset = true
		}
		if len(message.Events) > connectionStreamLimit {
			return errors.New("连接统计流事件超限")
		}
		if message.Reset_ {
			clear(tracked)
		}
		batch := make([]ConnectionSample, 0, len(message.Events))
		for _, event := range message.Events {
			sample, ok, err := applyStreamEvent(tracked, event)
			if err != nil {
				return err
			}
			if ok {
				batch = append(batch, sample)
			}
		}
		if err := publish(batch, message.Reset_); err != nil {
			return err
		}
	}
}

func connectionStreamAddressOK(address string) bool {
	host, port, err := net.SplitHostPort(address)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback() && ip.Zone() == ""
}

func applyStreamEvent(tracked map[string]streamTracked, event *streamEvent) (ConnectionSample, bool, error) {
	if event == nil || event.ID == "" || len(event.ID) > 128 {
		return ConnectionSample{}, false, errors.New("连接统计流事件无效")
	}
	switch event.Type {
	case 0: // NEW, including closed records in a reset snapshot.
		if event.Connection == nil || event.Connection.ID != event.ID {
			return ConnectionSample{}, false, errors.New("连接统计流身份无效")
		}
		sample, ok, err := connectionSample(event.Connection)
		if err != nil {
			return ConnectionSample{}, false, err
		}
		if event.Connection.ClosedAt == 0 {
			if _, exists := tracked[event.ID]; !exists && len(tracked) >= connectionStreamLimit {
				return ConnectionSample{}, false, errors.New("连接统计流容量超限")
			}
			tracked[event.ID] = streamTracked{sample: sample, valid: ok}
		}
		return sample, ok, nil
	case 1: // UPDATE carries deltas only; metadata comes from NEW.
		entry, found := tracked[event.ID]
		if !found || event.UplinkDelta < 0 || event.DownlinkDelta < 0 || event.UplinkDelta > math.MaxInt64-entry.sample.Upload || event.DownlinkDelta > math.MaxInt64-entry.sample.Download {
			return ConnectionSample{}, false, errors.New("连接统计流增量无效")
		}
		entry.sample.Upload += event.UplinkDelta
		entry.sample.Download += event.DownlinkDelta
		tracked[event.ID] = entry
		return entry.sample, entry.valid, nil
	case 2: // CLOSED includes final cumulative totals and metadata.
		prior, hadPrior := tracked[event.ID]
		delete(tracked, event.ID)
		if event.Connection == nil || event.Connection.ID != event.ID {
			return ConnectionSample{}, false, errors.New("连接统计流关闭记录不完整")
		}
		sample, ok, err := connectionSample(event.Connection)
		if err != nil {
			return ConnectionSample{}, false, err
		}
		if hadPrior && (sample.Upload < prior.sample.Upload || sample.Download < prior.sample.Download) {
			return ConnectionSample{}, false, errors.New("连接统计流关闭计数回退")
		}
		if hadPrior && prior.valid && ok && (sample.AuthUser != prior.sample.AuthUser || sample.Domain != prior.sample.Domain) {
			return ConnectionSample{}, false, errors.New("连接统计流归属变化")
		}
		if hadPrior && prior.valid && !ok {
			sample.AuthUser, sample.Domain, sample.Destination = prior.sample.AuthUser, prior.sample.Domain, prior.sample.Destination
			ok = true
		}
		if event.ClosedAt > 0 {
			sample.ClosedAt = time.UnixMilli(event.ClosedAt)
		}
		return sample, ok, nil
	default:
		return ConnectionSample{}, false, errors.New("连接统计流事件类型无效")
	}
}

func connectionSample(c *streamConnection) (ConnectionSample, bool, error) {
	if c == nil || c.ID == "" || c.CreatedAt <= 0 || c.UplinkTotal < 0 || c.DownlinkTotal < 0 || c.ClosedAt < 0 {
		return ConnectionSample{}, false, errors.New("连接统计流计数无效")
	}
	domain := validStreamTarget(c.Domain)
	if domain == "" {
		domain = validStreamTarget(streamDestinationHost(c.Destination))
	}
	sample := ConnectionSample{
		ID: fmt.Sprintf("%d:%s", c.CreatedAt, c.ID), AuthUser: c.AuthUser,
		Domain: domain, Destination: boundedStreamDestination(c.Destination),
		StartedAt: time.UnixMilli(c.CreatedAt), Upload: c.UplinkTotal, Download: c.DownlinkTotal,
	}
	if c.ClosedAt > 0 {
		sample.ClosedAt = time.UnixMilli(c.ClosedAt)
	}
	return sample, c.AuthUser != "" && len(c.AuthUser) <= 1024 && domain != "", nil
}

func streamDestinationHost(destination string) string {
	if host, _, err := net.SplitHostPort(destination); err == nil {
		return host
	}
	return ""
}

func validStreamTarget(raw string) string {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if ip, err := netip.ParseAddr(domain); err == nil {
		if ip.Zone() != "" {
			return ""
		}
		return ip.String()
	}
	if len(domain) == 0 || len(domain) > 253 || strings.ContainsAny(domain, " /\\:@\x00\r\n\t") {
		return ""
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, char := range label {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
				return ""
			}
		}
	}
	return domain
}

func boundedStreamDestination(destination string) string {
	if len(destination) > 512 || strings.ContainsAny(destination, "\x00\r\n") {
		return ""
	}
	return destination
}
