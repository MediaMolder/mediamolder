// Mirror of pipeline/config.go types. Keep field names in sync.
// Only fields used by the editor are typed; unknown JSON fields are preserved
// via passthrough during round-trip in adapter.ts.

export type StreamType = 'video' | 'audio' | 'subtitle' | 'data';

export interface StreamSelect {
  input_index: number;
  type: StreamType;
  track: number;
}

export interface Input {
  id: string;
  url: string;
  streams: StreamSelect[];
  options?: Record<string, unknown>;
}

export interface NodeDef {
  id: string;
  type: string; // "filter" | "encoder" | "source" | "sink" | "go_processor"
  filter?: string;
  processor?: string;
  params?: Record<string, unknown>;
  error_policy?: ErrorPolicy;
}

export interface EdgeDef {
  from: string; // "nodeID:port" or "inputID:v:0"
  to: string;
  type: StreamType;
}

export interface GraphDef {
  nodes: NodeDef[];
  edges: EdgeDef[];
}

export interface Output {
  id: string;
  url: string;
  format?: string;
  codec_video?: string;
  codec_audio?: string;
  codec_subtitle?: string;
  bsf_video?: string;
  bsf_audio?: string;
  options?: Record<string, unknown>;
}

export interface Options {
  threads?: number;
  thread_type?: string;
  hw_accel?: string;
  hw_device?: string;
  realtime?: boolean;
}

export interface ErrorPolicy {
  policy: string;
  max_retries?: number;
  fallback_node?: string;
}

export interface JobConfig {
  schema_version: string;
  description?: string;
  inputs: Input[];
  graph: GraphDef;
  outputs: Output[];
  global_options?: Options;
}
