// Copyright 2016 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: David Taylor (david@cockroachlabs.com)

package acceptance

import (
	"fmt"
	"testing"
)

func runReadWriteReferenceTest(
	t *testing.T,
	referenceBinPath string,
	backwardReferenceTest string,
) {
	if err := testDockerSingleNode(t, "reference", []string{"/cockroach", "version"}); err != nil {
		t.Skipf(`TODO(dt): No /cockroach binary in one-shot container, see #6086: %s`, err)
	}
	referenceTestScript := fmt.Sprintf(`
set -xe
mkdir /old
cd /old

export PGHOST=localhost
export PGPORT=""

bin=/%s/cockroach
# TODO(bdarnell): when --background is in referenceBinPath, use it here and below.
$bin start &
sleep 1
echo "Use the reference binary to write a couple rows, then render its output to a file and shut down."
$bin sql -e "CREATE DATABASE old"
$bin sql -d old -e "CREATE TABLE testing (i int primary key, b bool, s string unique, d decimal, f float, t timestamp, v interval, index sb (s, b))"
$bin sql -d old -e "INSERT INTO testing values (1, true, 'hello', decimal '3.14159', 3.14159, NOW(), interval '1h')"
$bin sql -d old -e "INSERT INTO testing values (2, false, 'world', decimal '0.14159', 0.14159, NOW(), interval '234h45m2s234ms')"
$bin sql -d old -e "SELECT i, b, s, d, f, v, extract(epoch FROM t) FROM testing" > old.everything
$bin quit && wait # wait will block until all background jobs finish.

bin=/cockroach
$bin start --background
echo "Read data written by reference version using new binary"
$bin sql -d old -e "SELECT i, b, s, d, f, v, extract(epoch FROM t) FROM testing" > new.everything
# diff returns non-zero if different. With set -e above, that would exit here.
diff new.everything old.everything

echo "Add a row with the new binary and render the updated data before shutting down."
$bin sql -d old -e "INSERT INTO testing values (3, false, '!', decimal '2.14159', 2.14159, NOW(), interval '3h')"
$bin sql -d old -e "SELECT i, b, s, d, f, v, extract(epoch FROM t) FROM testing" > new.everything
$bin quit
# Let it close its listening sockets.
sleep 1

echo "Read the modified data using the reference binary again."
bin=/%s/cockroach
%s
`, referenceBinPath, referenceBinPath, backwardReferenceTest)
	err := testDockerSingleNode(t, "reference", []string{"/bin/bash", "-c", referenceTestScript})
	if err != nil {
		t.Errorf("expected success: %s", err)
	}
}

func TestDockerReadWriteBidirectionalReferenceVersion(t *testing.T) {
	backwardReferenceTest := `
$bin start &
sleep 1
$bin sql -d old -e "SELECT i, b, s, d, f, v, extract(epoch FROM t) FROM testing" > old.everything
# diff returns non-zero if different. With set -e above, that would exit here.
diff new.everything old.everything
$bin quit && wait
`
	runReadWriteReferenceTest(t, `bidirectional-reference-version`, backwardReferenceTest)
}

func TestDockerReadWriteForwardReferenceVersion(t *testing.T) {
	backwardReferenceTest := `
# TODO(dan): Once we have a version that's only forward compatible, test the failure message.
`
	runReadWriteReferenceTest(t, `forward-reference-version`, backwardReferenceTest)
}
