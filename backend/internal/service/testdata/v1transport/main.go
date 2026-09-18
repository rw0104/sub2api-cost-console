// Minimal original-protocol fixture; deliberately does not import pluginapi/v2.
package main

import (
	"context"
	"io"
	"strings"

	v1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

type transport struct {
	v1.UnimplementedTransportPluginServer
}

func main() { v1.Serve(&transport{}) }
func (*transport) GetInfo(context.Context, *v1.GetInfoRequest) (*v1.GetInfoResponse, error) {
	return &v1.GetInfoResponse{PluginId: "com.example.openai-transport", PluginVersion: "0.1.0", ProtocolVersion: 1, TransportApiVersion: 1, Capabilities: []string{"openai.oauth.outbound_transport.v1"}}, nil
}
func (*transport) Health(context.Context, *v1.HealthRequest) (*v1.HealthResponse, error) {
	return &v1.HealthResponse{Healthy: true}, nil
}
func (*transport) ValidateConfig(_ context.Context, r *v1.ValidateConfigRequest) (*v1.ValidateConfigResponse, error) {
	return &v1.ValidateConfigResponse{Valid: true, NormalizedConfigJson: r.ConfigJson}, nil
}
func (*transport) ApplyConfig(context.Context, *v1.ApplyConfigRequest) (*v1.ApplyConfigResponse, error) {
	return &v1.ApplyConfigResponse{Applied: true}, nil
}
func (*transport) TestConfig(context.Context, *v1.TestConfigRequest) (*v1.TestConfigResponse, error) {
	return &v1.TestConfigResponse{Success: true}, nil
}
func (*transport) Forward(stream v1.TransportPlugin_ForwardServer) error {
	var body []byte
	var target string
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if start := frame.GetStart(); start != nil {
			target = start.Url
		}
		body = append(body, frame.GetBodyChunk()...)
		if frame.GetBodyEnd() {
			break
		}
	}
	if strings.HasSuffix(target, "/sent") {
		return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Error{Error: &v1.ForwardResponseError{Code: "UPSTREAM_FAILED", Message: "synthetic post-send failure", RequestSent: true}}})
	}
	if strings.HasSuffix(target, "/cancel") {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if err := stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_Start{Start: &v1.ForwardResponseStart{StatusCode: 200, Status: "200 OK", Protocol: "HTTP/1.1", ProtocolMajor: 1, ProtocolMinor: 1, ContentLength: int64(len(body))}}}); err != nil {
		return err
	}
	for _, b := range body {
		if err := stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_BodyChunk{BodyChunk: []byte{b}}}); err != nil {
			return err
		}
	}
	return stream.Send(&v1.ForwardResponse{Frame: &v1.ForwardResponse_End{End: &v1.ForwardResponseEnd{BytesReceived: int64(len(body))}}})
}
