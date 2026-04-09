import os, textwrap

def w(path, src):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        f.write(src)
    print('wrote', path)

BASE = '/Users/tom.vaughan/mediamolder'

# ── cmd/mediamolder/main.go ──────────────────────────────────────────────────
w(BASE+'/cmd/mediamolder/main.go', textwrap.dedent('''\
package main

import (
\t"context"
\t"encoding/json"
\t"flag"
\t"fmt"
\t"os"

\t"github.com/MediaMolder/MediaMolder/av"
\t"github.com/MediaMolder/MediaMolder/pipeline"
)

func main() {
\tif err := run(os.Args[1:]); err != nil {
\t\tfmt.Fprintf(os.Stderr, "mediamolder: %v\\n", err)
\t\tos.Exit(1)
\t}
}

func run(args []string) error {
\tif len(args) == 0 {
\t\tusage()
\t\treturn nil
\t}
\tswitch args[0] {
\tcase "run":
\t\treturn cmdRun(args[1:])
\tcase "inspect":
\t\treturn cmdInspect(args[1:])
\tcase "version":
\t\tfmt.Println("mediamolder dev (" + av.LibVersions() + ")")
\t\treturn nil
\tcase "help", "--help", "-h":
\t\tusage()
\t\treturn nil
\tdefault:
\t\treturn fmt.Errorf("unknown command %q\\nRun \\'mediamolder help\\' for usage.", args[0])
\t}
}

func cmdRun(args []string) error {
\tfs := flag.NewFlagSet("run", flag.ContinueOnError)
\tif err := fs.Parse(args); err != nil {
\t\treturn err
\t}
\tif fs.NArg() < 1 {
\t\treturn fmt.Errorf("usage: mediamolder run <config.json>")
\t}
\tcfg, err := pipeline.ParseConfigFile(fs.Arg(0))
\tif err != nil {
\t\treturn err
\t}
\teng, err := pipeline.NewEngine(cfg)
\tif err != nil {
\t\treturn err
\t}
\treturn eng.Run(context.Background())
}

func cmdInspect(args []string) error {
\tfs := flag.NewFlagSet("inspect", flag.ContinueOnError)
\tif err := fs.Parse(args); err != nil {
\t\treturn err
\t}
\tif fs.NArg() < 1 {
\t\treturn fmt.Errorf("usage: mediamolder inspect <config.json>")
\t}
\tcfg, err := pipeline.ParseConfigFile(fs.Arg(0))
\tif err != nil {
\t\treturn err
\t}
\tenc := json.NewEncoder(os.Stdout)
\tenc.SetIndent("", "  ")
\treturn enc.Encode(cfg)
}

func usage() {
\tfmt.Print(`Usage: mediamolder <command> [args]

Commands:
  run <config.json>      Execute a pipeline from a JSON config file.
  inspect <config.json>  Validate and pretty-print the resolved pipeline config.
  version                Print library versions.
  help                   Show this help.
`)
}
'''))

# ── pipeline/config_test.go ──────────────────────────────────────────────────
w(BASE+'/pipeline/config_test.go', textwrap.dedent('''\
package pipeline

import (
\t"encoding/json"
\t"testing"
)

var validConfig = `{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "streams": [
        {"input_index": 0, "type": "video", "track": 0}
      ]
    }
  ],
  "graph": {
    "nodes": [
      {
        "id": "scale",
        "type": "filter",
        "filter": "scale",
        "params": {"width": 1280, "height": 720}
      }
    ],
    "edges": [
      {"from": "src:v:0", "to": "scale:default", "type": "video"},
      {"from": "scale:default", "to": "out:v", "type": "video"}
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "libx264"
    }
  ]
}`

func TestParseValidConfig(t *testing.T) {
\tcfg, err := ParseConfig([]byte(validConfig))
\tif err != nil {
\t\tt.Fatalf("ParseConfig: %v", err)
\t}
\tif cfg.SchemaVersion != "1.0" {
\t\tt.Errorf("schema_version = %q, want 1.0", cfg.SchemaVersion)
\t}
\tif len(cfg.Inputs) != 1 {
\t\tt.Errorf("len(inputs) = %d, want 1", len(cfg.Inputs))
\t}
\tif cfg.Inputs[0].ID != "src" {
\t\tt.Errorf("input id = %q, want src", cfg.Inputs[0].ID)
\t}
\tif len(cfg.Graph.Nodes) != 1 {
\t\tt.Errorf("len(nodes) = %d, want 1", len(cfg.Graph.Nodes))
\t}
\tif len(cfg.Graph.Edges) != 2 {
\t\tt.Errorf("len(edges) = %d, want 2", len(cfg.Graph.Edges))
\t}
}

func TestParseConfigMissingSchemaVersion(t *testing.T) {
\tvar m map[string]interface{}
\tjson.Unmarshal([]byte(validConfig), &m)
\tdelete(m, "schema_version")
\tdata, _ := json.Marshal(m)
\t_, err := ParseConfig(data)
\tif err == nil {
\t\tt.Fatal("expected error for missing schema_version, got nil")
\t}
}

func TestParseConfigWrongSchemaVersion(t *testing.T) {
\tbad := `{"schema_version":"2.0","inputs":[{"id":"a","url":"a","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"b"}]}`
\t_, err := ParseConfig([]byte(bad))
\tif err == nil {
\t\tt.Fatal("expected error for schema_version 2.0")
\t}
}

func TestParseConfigRejectsUnknownFields(t *testing.T) {
\tbad := `{"schema_version":"1.0","unknown_field":true,"inputs":[{"id":"a","url":"a","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"b"}]}`
\t_, err := ParseConfig([]byte(bad))
\tif err == nil {
\t\tt.Fatal("expected error for unknown field")
\t}
}

func TestParseConfigNoDuplicateIDs(t *testing.T) {
\tbad := `{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]},{"id":"a","url":"y","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`
\t_, err := ParseConfig([]byte(bad))
\tif err == nil {
\t\tt.Fatal("expected error for duplicate input id")
\t}
}

func TestParseConfigInvalidEdgeType(t *testing.T) {
\tbad := `{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[{"from":"a:v:0","to":"out:v","type":"bogus"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`
\t_, err := ParseConfig([]byte(bad))
\tif err == nil {
\t\tt.Fatal("expected error for invalid edge type")
\t}
}

func TestFilterSpecBuilder(t *testing.T) {
\tnode := NodeDef{
\t\tID:     "scale",
\t\tType:   "filter",
\t\tFilter: "scale",
\t\tParams: map[string]any{"width": 1280, "height": 720},
\t}
\tspec := buildFilterSpec(node)
\tif spec == "" || spec == "null" {
\t\tt.Errorf("unexpected empty filter spec")
\t}
\tif len(spec) < 5 {
\t\tt.Errorf("filter spec too short: %q", spec)
\t}
}
'''))

# ── av/version_test.go ───────────────────────────────────────────────────────
w(BASE+'/av/version_test.go', textwrap.dedent('''\
package av

import "testing"

func TestCheckVersion(t *testing.T) {
\tif err := CheckVersion(); err != nil {
\t\tt.Errorf("CheckVersion() = %v; want nil (FFmpeg 8.1+ required)", err)
\t}
}

func TestLibVersions(t *testing.T) {
\ts := LibVersions()
\tif s == "" {
\t\tt.Error("LibVersions() returned empty string")
\t}
\tt.Log("linked libraries:", s)
}
'''))

# ── av/err_test.go ───────────────────────────────────────────────────────────
w(BASE+'/av/err_test.go', textwrap.dedent('''\
package av

import (
\t"errors"
\t"testing"
)

func TestErrError(t *testing.T) {
\te := &Err{Code: -1, Message: "test error"}
\tif e.Error() == "" {
\t\tt.Error("Err.Error() returned empty string")
\t}
}

func TestIsEOF(t *testing.T) {
\tif !IsEOF(ErrEOF) {
\t\tt.Error("IsEOF(ErrEOF) = false; want true")
\t}
\tif IsEOF(errors.New("not eof")) {
\t\tt.Error("IsEOF(other) = true; want false")
\t}
}
'''))

print('All files written.')
