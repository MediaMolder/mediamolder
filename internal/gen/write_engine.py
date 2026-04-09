import os, textwrap

def w(path, src):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w') as f:
        f.write(src)
    print('wrote', path)

BASE = '/Users/tom.vaughan/mediamolder'

# ── go.mod (add golang.org/x/sync) ──────────────────────────────────────────
# handled via go get after this script

# ── pipeline/engine.go ──────────────────────────────────────────────────────
w(BASE+'/pipeline/engine.go', textwrap.dedent('''\
package pipeline

import (
\t"context"
\t"fmt"
\t"sync"

\t"github.com/MediaMolder/MediaMolder/av"
\t"golang.org/x/sync/errgroup"
)

// Engine executes a linear single-input -> filter -> single-output pipeline.
type Engine struct {
\tcfg *Config

\tmu     sync.Mutex
\tstate  State
\tcancel context.CancelFunc
\teg     *errgroup.Group
}

// NewEngine creates an Engine from a validated Config.
func NewEngine(cfg *Config) (*Engine, error) {
\tif err := av.CheckVersion(); err != nil {
\t\treturn nil, err
\t}
\treturn &Engine{cfg: cfg, state: StateNull}, nil
}

// State returns the current pipeline state.
func (e *Engine) State() State {
\te.mu.Lock()
\tdefer e.mu.Unlock()
\treturn e.state
}

// Run executes the pipeline to completion and blocks until done.
func (e *Engine) Run(ctx context.Context) error {
\te.mu.Lock()
\tif e.state != StateNull {
\t\te.mu.Unlock()
\t\treturn fmt.Errorf("Run called on non-NULL pipeline (state=%s)", e.state)
\t}
\tctx, cancel := context.WithCancel(ctx)
\te.cancel = cancel
\tg, ctx := errgroup.WithContext(ctx)
\te.eg = g
\te.state = StatePlaying
\te.mu.Unlock()

\tdefer func() {
\t\te.mu.Lock()
\t\te.state = StateNull
\t\te.mu.Unlock()
\t\tcancel()
\t}()
\treturn e.runLinear(ctx, g)
}

// Close cancels a running pipeline and waits for goroutines to exit.
func (e *Engine) Close() error {
\te.mu.Lock()
\tcancel := e.cancel
\te.mu.Unlock()
\tif cancel != nil {
\t\tcancel()
\t}
\tif e.eg != nil {
\t\treturn e.eg.Wait()
\t}
\treturn nil
}

func (e *Engine) runLinear(ctx context.Context, g *errgroup.Group) error {
\tcfg := e.cfg
\tinCfg := cfg.Inputs[0]
\toutCfg := cfg.Outputs[0]

\tinput, err := av.OpenInput(inCfg.URL, nil)
\tif err != nil {
\t\treturn fmt.Errorf("open input %q: %w", inCfg.URL, err)
\t}
\tdefer input.Close()

\t// Resolve the first requested video stream.
\tvidIdx := -1
\tall, _ := input.AllStreams()
\tfor _, sel := range inCfg.Streams {
\t\tif sel.Type != "video" {
\t\t\tcontinue
\t\t}
\t\tcount := 0
\t\tfor _, si := range all {
\t\t\tif si.Type == av.MediaTypeVideo {
\t\t\t\tif count == sel.Track {
\t\t\t\t\tvidIdx = si.Index
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tcount++
\t\t\t}
\t\t}
\t\tif vidIdx >= 0 {
\t\t\tbreak
\t\t}
\t}
\tif vidIdx < 0 {
\t\treturn fmt.Errorf("no video stream found in %q", inCfg.URL)
\t}

\tsi, _ := input.StreamInfo(vidIdx)

\tdec, err := av.OpenDecoder(input, vidIdx)
\tif err != nil {
\t\treturn fmt.Errorf("open decoder: %w", err)
\t}
\tdefer dec.Close()

\tfilterSpec := "null"
\tfor _, node := range cfg.Graph.Nodes {
\t\tif node.Type == "filter" && node.Filter != "" {
\t\t\tfilterSpec = buildFilterSpec(node)
\t\t\tbreak
\t\t}
\t}

\tfg, err := av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
\t\tWidth:      si.Width,
\t\tHeight:     si.Height,
\t\tPixFmt:     0,
\t\tTBNum:      si.TimeBase[0],
\t\tTBDen:      si.TimeBase[1],
\t\tSARNum:     1,
\t\tSARDen:     1,
\t\tFilterSpec: filterSpec,
\t})
\tif err != nil {
\t\treturn fmt.Errorf("build filter graph %q: %w", filterSpec, err)
\t}
\tdefer fg.Close()

\tenc, err := av.OpenEncoder(av.EncoderOptions{
\t\tCodecName:    outCfg.CodecVideo,
\t\tWidth:        si.Width,
\t\tHeight:       si.Height,
\t\tFrameRate:    [2]int{25, 1},
\t\tGlobalHeader: true,
\t\tExtraOpts:    map[string]string{"preset": "medium"},
\t})
\tif err != nil {
\t\treturn fmt.Errorf("open encoder %q: %w", outCfg.CodecVideo, err)
\t}
\tdefer enc.Close()

\tmuxer, err := av.OpenOutput(outCfg.URL)
\tif err != nil {
\t\treturn fmt.Errorf("open muxer %q: %w", outCfg.URL, err)
\t}
\tsuccess := false
\tdefer func() {
\t\tif !success {
\t\t\tmuxer.Abort()
\t\t}
\t}()

\tif _, err := muxer.AddStream(enc); err != nil {
\t\treturn fmt.Errorf("add stream: %w", err)
\t}
\tif err := muxer.WriteHeader(); err != nil {
\t\treturn fmt.Errorf("write header: %w", err)
\t}

\tpktCh := make(chan *av.Packet, 8)
\tdecFrameCh := make(chan *av.Frame, 8)
\tfiltFrameCh := make(chan *av.Frame, 8)
\tencPktCh := make(chan *av.Packet, 8)

\t// Demux stage.
\tg.Go(func() error {
\t\tdefer close(pktCh)
\t\tfor {
\t\t\tif ctx.Err() != nil {
\t\t\t\treturn ctx.Err()
\t\t\t}
\t\t\tpkt, err := av.AllocPacket()
\t\t\tif err != nil {
\t\t\t\treturn err
\t\t\t}
\t\t\tif err := input.ReadPacket(pkt); err != nil {
\t\t\t\tpkt.Close()
\t\t\t\tif av.IsEOF(err) {
\t\t\t\t\treturn nil
\t\t\t\t}
\t\t\t\treturn err
\t\t\t}
\t\t\tif pkt.StreamIndex() != vidIdx {
\t\t\t\tpkt.Close()
\t\t\t\tcontinue
\t\t\t}
\t\t\tselect {
\t\t\tcase pktCh <- pkt:
\t\t\tcase <-ctx.Done():
\t\t\t\tpkt.Close()
\t\t\t\treturn ctx.Err()
\t\t\t}
\t\t}
\t})

\t// Decode stage.
\tg.Go(func() error {
\t\tdefer close(decFrameCh)
\t\tdrainDecoder := func() error {
\t\t\tif err := dec.Flush(); err != nil {
\t\t\t\treturn err
\t\t\t}
\t\t\tfor {
\t\t\t\tf, _ := av.AllocFrame()
\t\t\t\terr := dec.ReceiveFrame(f)
\t\t\t\tif av.IsEOF(err) {
\t\t\t\t\tf.Close()
\t\t\t\t\treturn nil
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tf.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase decFrameCh <- f:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tf.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\tfor pkt := range pktCh {
\t\t\tif err := dec.SendPacket(pkt); err != nil {
\t\t\t\tpkt.Close()
\t\t\t\treturn err
\t\t\t}
\t\t\tpkt.Close()
\t\t\tfor {
\t\t\t\tf, _ := av.AllocFrame()
\t\t\t\terr := dec.ReceiveFrame(f)
\t\t\t\tif av.IsEAgain(err) {
\t\t\t\t\tf.Close()
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tf.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase decFrameCh <- f:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tf.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\treturn drainDecoder()
\t})

\t// Filter stage.
\tg.Go(func() error {
\t\tdefer close(filtFrameCh)
\t\tdrainFilter := func() error {
\t\t\tif err := fg.Flush(); err != nil && !av.IsEOF(err) {
\t\t\t\treturn err
\t\t\t}
\t\t\tfor {
\t\t\t\tf, _ := av.AllocFrame()
\t\t\t\terr := fg.PullFrame(f)
\t\t\t\tif av.IsEOF(err) || av.IsEAgain(err) {
\t\t\t\t\tf.Close()
\t\t\t\t\treturn nil
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tf.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase filtFrameCh <- f:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tf.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\tfor f := range decFrameCh {
\t\t\tif err := fg.PushFrame(f); err != nil {
\t\t\t\tf.Close()
\t\t\t\treturn err
\t\t\t}
\t\t\tf.Close()
\t\t\tfor {
\t\t\t\tout, _ := av.AllocFrame()
\t\t\t\terr := fg.PullFrame(out)
\t\t\t\tif av.IsEAgain(err) {
\t\t\t\t\tout.Close()
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tout.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase filtFrameCh <- out:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tout.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\treturn drainFilter()
\t})

\t// Encode stage.
\tg.Go(func() error {
\t\tdefer close(encPktCh)
\t\tdrainEncoder := func() error {
\t\t\tif err := enc.Flush(); err != nil {
\t\t\t\treturn err
\t\t\t}
\t\t\tfor {
\t\t\t\tp, _ := av.AllocPacket()
\t\t\t\terr := enc.ReceivePacket(p)
\t\t\t\tif av.IsEOF(err) {
\t\t\t\t\tp.Close()
\t\t\t\t\treturn nil
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tp.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase encPktCh <- p:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tp.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\tfor f := range filtFrameCh {
\t\t\tif err := enc.SendFrame(f); err != nil {
\t\t\t\tf.Close()
\t\t\t\treturn err
\t\t\t}
\t\t\tf.Close()
\t\t\tfor {
\t\t\t\tp, _ := av.AllocPacket()
\t\t\t\terr := enc.ReceivePacket(p)
\t\t\t\tif av.IsEAgain(err) {
\t\t\t\t\tp.Close()
\t\t\t\t\tbreak
\t\t\t\t}
\t\t\t\tif err != nil {
\t\t\t\t\tp.Close()
\t\t\t\t\treturn err
\t\t\t\t}
\t\t\t\tselect {
\t\t\t\tcase encPktCh <- p:
\t\t\t\tcase <-ctx.Done():
\t\t\t\t\tp.Close()
\t\t\t\t\treturn ctx.Err()
\t\t\t\t}
\t\t\t}
\t\t}
\t\treturn drainEncoder()
\t})

\t// Mux stage.
\tg.Go(func() error {
\t\tfor pkt := range encPktCh {
\t\t\tpkt.SetStreamIndex(0)
\t\t\tif err := muxer.WritePacket(pkt); err != nil {
\t\t\t\tpkt.Close()
\t\t\t\treturn err
\t\t\t}
\t\t\tpkt.Close()
\t\t}
\t\treturn muxer.WriteTrailer()
\t})

\tif err := g.Wait(); err != nil {
\t\treturn err
\t}
\tsuccess = true
\treturn muxer.Close()
}

func buildFilterSpec(node NodeDef) string {
\tif node.Filter == "" {
\t\treturn "null"
\t}
\tif len(node.Params) == 0 {
\t\treturn node.Filter
\t}
\tspec := node.Filter
\tfirst := true
\tfor k, v := range node.Params {
\t\tif first {
\t\t\tspec += "="
\t\t\tfirst = false
\t\t} else {
\t\t\tspec += ":"
\t\t}
\t\tspec += fmt.Sprintf("%s=%v", k, v)
\t}
\treturn spec
}
'''))

print('Done')
