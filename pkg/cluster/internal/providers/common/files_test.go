/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

const testSecret = "-----BEGIN RSA PRIVATE KEY-----\nTOP-SECRET-MATERIAL\n-----END RSA PRIVATE KEY-----\n"

// recordingCmder records commands and their stdin; Run fails when fail is set,
// with a RunError carrying the command line and some output like a real one.
type recordingCmder struct {
	fail bool
	cmds []*recordingCmd
}

type recordingCmd struct {
	cmder *recordingCmder
	argv  []string
	stdin []byte
}

func (c *recordingCmder) Command(name string, args ...string) exec.Cmd {
	cmd := &recordingCmd{cmder: c, argv: append([]string{name}, args...)}
	c.cmds = append(c.cmds, cmd)
	return cmd
}

func (c *recordingCmder) CommandContext(_ context.Context, name string, args ...string) exec.Cmd {
	return c.Command(name, args...)
}

func (c *recordingCmd) SetEnv(...string) exec.Cmd    { return c }
func (c *recordingCmd) SetStdout(io.Writer) exec.Cmd { return c }
func (c *recordingCmd) SetStderr(io.Writer) exec.Cmd { return c }
func (c *recordingCmd) SetStdin(r io.Reader) exec.Cmd {
	c.stdin, _ = io.ReadAll(r)
	return c
}
func (c *recordingCmd) Run() error {
	if c.cmder.fail {
		return &exec.RunError{Command: c.argv, Output: []byte("mv: cannot move: Permission denied"), Inner: errors.New("exit status 1")}
	}
	return nil
}

func TestWriteNodeFilesContentOnlyOnStdin(t *testing.T) {
	cmder := &recordingCmder{}
	files := []config.NodeFile{
		{Path: "/etc/kubernetes/pki/ca.key", Content: testSecret},
		{Path: "/etc/kubernetes/pki/ca.crt", Content: "cert", Permissions: "0644"},
	}
	if err := WriteNodeFiles(cmder, "docker", "kind-control-plane", files); err != nil {
		t.Fatalf("WriteNodeFiles: %v", err)
	}
	if len(cmder.cmds) != 2 {
		t.Fatalf("ran %d commands, want 2", len(cmder.cmds))
	}
	for i, cmd := range cmder.cmds {
		argv := strings.Join(cmd.argv, " ")
		if strings.Contains(argv, "TOP-SECRET") || strings.Contains(argv, "cert\n") {
			t.Errorf("command %d carries file content in argv: %q", i, argv)
		}
		if got := cmd.argv[:4]; strings.Join(got, " ") != "docker exec -i kind-control-plane" {
			t.Errorf("command %d prefix = %v", i, got)
		}
		if string(cmd.stdin) != files[i].Content {
			t.Errorf("command %d stdin = %q, want the file content", i, cmd.stdin)
		}
		if cmd.argv[len(cmd.argv)-2] != files[i].Path {
			t.Errorf("command %d path arg = %q", i, cmd.argv[len(cmd.argv)-2])
		}
	}
	if perms := cmder.cmds[0].argv[len(cmder.cmds[0].argv)-1]; perms != config.DefaultNodeFilePermissions {
		t.Errorf("default permissions = %q, want %q", perms, config.DefaultNodeFilePermissions)
	}
	if perms := cmder.cmds[1].argv[len(cmder.cmds[1].argv)-1]; perms != "0644" {
		t.Errorf("permissions = %q, want 0644", perms)
	}
}

func TestWriteNodeFilesErrorOmitsContent(t *testing.T) {
	cmder := &recordingCmder{fail: true}
	err := WriteNodeFiles(cmder, "podman", "n", []config.NodeFile{{Path: "/etc/secret", Content: testSecret}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "/etc/secret") {
		t.Errorf("error does not name the path: %v", err)
	}
	if strings.Contains(err.Error(), "TOP-SECRET") || strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("error leaks file content: %v", err)
	}
}

// localCmder runs "<runtime> exec -i <container> <cmd...>" as "<cmd...>" on
// the local host, so the write script itself can be exercised without a
// container runtime.
type localCmder struct{}

func (localCmder) Command(name string, args ...string) exec.Cmd {
	return exec.Command(args[3], args[4:]...)
}

func (l localCmder) CommandContext(_ context.Context, name string, args ...string) exec.Cmd {
	return l.Command(name, args...)
}

func TestWriteNodeFilesScript(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "pki", "nested", "ca.key")
	certPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(certPath, []byte("old"), 0o666); err != nil {
		t.Fatal(err)
	}
	files := []config.NodeFile{
		{Path: keyPath, Content: testSecret},
		{Path: certPath, Content: "new cert\n", Permissions: "0644"},
	}
	if err := WriteNodeFiles(localCmder{}, "docker", "node", files); err != nil {
		t.Fatalf("WriteNodeFiles: %v", err)
	}
	for _, want := range []struct {
		path    string
		content string
		mode    os.FileMode
	}{{keyPath, testSecret, 0o600}, {certPath, "new cert\n", 0o644}} {
		got, err := os.ReadFile(want.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want.content {
			t.Errorf("%s content = %q, want %q", want.path, got, want.content)
		}
		st, err := os.Stat(want.path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want.mode {
			t.Errorf("%s mode = %v, want %v", want.path, st.Mode().Perm(), want.mode)
		}
	}
	// no temporary files are left behind
	for _, d := range []string{dir, filepath.Dir(keyPath)} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), ".") && e.Name() != "ca.key" && e.Name() != "ca.crt" {
				t.Errorf("leftover file %s in %s", e.Name(), d)
			}
		}
	}
}
