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
	"strings"

	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

// writeNodeFileScript writes stdin to the file "$1" with permissions "$2".
// The file is created under umask 077 as a temporary file next to the
// target, chmod-ed and then renamed into place, so it is never readable
// with wider permissions than requested and never left half-written.
const writeNodeFileScript = `set -e
mkdir -p "$(dirname "$1")"
umask 077
tmp="$(mktemp "$1.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
cat > "$tmp"
chmod "$2" "$tmp"
mv -f "$tmp" "$1"
trap - EXIT
`

// WriteNodeFiles writes files into the node container using the container
// runtime CLI binary (docker, podman, nerdctl). Each file's content is
// streamed over the exec's stdin: it never appears in the command line (and
// so never in process listings or the runtime's exec events and inspect
// output), and errors name only the file path, never its content.
func WriteNodeFiles(cmder exec.Cmder, binaryName, containerName string, files []config.NodeFile) error {
	for _, f := range files {
		perms := f.Permissions
		if perms == "" {
			perms = config.DefaultNodeFilePermissions
		}
		cmd := cmder.Command(binaryName,
			"exec", "-i", containerName,
			"sh", "-c", writeNodeFileScript, "sh", f.Path, perms,
		).SetStdin(strings.NewReader(f.Content))
		if err := cmd.Run(); err != nil {
			// The RunError carries only the command line (script and path)
			// and the command's output; neither contains the content.
			return errors.Wrapf(err, "failed to write post-create file %s", f.Path)
		}
	}
	return nil
}
