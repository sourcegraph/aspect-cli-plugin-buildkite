package main

import (
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/sourcegraph/aspect-cli-plugin-buildkite/bazel/bytestream"
	"google.golang.org/grpc"
)

var bytestreamConns = map[string]*bytestream.Client{}

func newBytestreamClient(ctx context.Context, uri string) (*bytestream.Client, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	cl, ok := bytestreamConns[u.Host]
	if !ok {
		conn, err := grpc.DialContext(ctx, u.Host, grpc.WithInsecure())
		if err != nil {
			return nil, err
		}
		bytestreamConns[u.Host] = cl
		return bytestream.NewClient(conn), nil
	}
	return cl, nil
}

func UploadBytestream(ctx context.Context, uri string, name string, agent BuildkiteAgent) error {
	cl, err := newBytestreamClient(ctx, uri)
	if err != nil {
		return err
	}
	u, err := url.Parse(uri)
	if err != nil {
		return err
	}
	r, err := cl.NewReader(ctx, u.Path)
	if err != nil {
		return err
	}
	path, err := os.MkdirTemp(".", "_bk_artefacts_")
	if err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(path, name))
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return agent.UploadArtifacts(ctx, filepath.Join(path, name))
}

// TODO Why does this crash?
func CloseBytestreamClients() {
	for _, cl := range bytestreamConns {
		cl.Close()
	}
}
