package outputfile

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/sourcegraph/aspect-cli-plugin-buildkite/bazel/bytestream"
	"google.golang.org/grpc"
)

type Client struct {
	bytestreamConns map[string]*grpc.ClientConn
}

func NewClient() *Client {
	return &Client{
		bytestreamConns: map[string]*grpc.ClientConn{},
	}
}

func (c *Client) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "file":
		return c.fileReader(u)
	case "bytestream":
		return c.bytestreamReader(ctx, u)
	default:
		return nil, fmt.Errorf("scheme not implemented %q (%q)", u.Scheme, u.String())
	}
}

func (c *Client) GetFilePath(ctx context.Context, uri string, name string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "file":
		// If it's a file, we just return the path on the local filesystem.
		return u.Path, nil
	case "bytestream":
		// If it's a bytestream, we need to fetch it and put it somewhere in the
		// local filesystem.
		rc, err := c.bytestreamReader(ctx, u)
		if err != nil {
			return "", err
		}
		defer rc.Close()
		path, err := os.MkdirTemp(".", "_bk_artefacts_")
		if err != nil {
			return "", err
		}
		outputPath := filepath.Join(path, name)
		f, err := os.Create(outputPath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := io.Copy(f, rc); err != nil {
			return "", err
		}
		if err := f.Sync(); err != nil {
			return "", err
		}
		return outputPath, nil
	default:
		return "", fmt.Errorf("scheme not implemented %q (%q)", u.Scheme, u.String())
	}

}

func (c *Client) Close() {
	for _, conn := range c.bytestreamConns {
		conn.Close()
	}
}

func (c *Client) bytestreamClient(ctx context.Context, uri *url.URL) (*bytestream.Client, error) {
	conn, ok := c.bytestreamConns[uri.Host]
	if !ok {
		var err error
		conn, err = grpc.DialContext(ctx, uri.Host, grpc.WithInsecure())
		if err != nil {
			return nil, err
		}
		c.bytestreamConns[uri.Host] = conn
	}
	return bytestream.NewClient(conn), nil
}

func (c *Client) bytestreamReader(ctx context.Context, uri *url.URL) (io.ReadCloser, error) {
	cl, err := c.bytestreamClient(ctx, uri)
	if err != nil {
		return nil, err
	}
	return cl.NewReader(ctx, uri.Path)
}

func (c *Client) fileReader(uri *url.URL) (io.ReadCloser, error) {
	return os.Open(uri.Path)
}
