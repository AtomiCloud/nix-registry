package seed

// Shared trace data model: made-up traces handed to the in-process Tempo engine.
// One TTrace per request; spans keep their parent link so structural TraceQL
// operators (>>, >, ~, <<, <, !>> ...) can be answered.

type TAttrVal struct {
	Kind int8 // 0 string, 1 int, 2 float, 3 bool
	S    string
	I    int64
	F    float64
	B    bool
}

func TStr(s string) TAttrVal    { return TAttrVal{Kind: 0, S: s} }
func TInt(i int64) TAttrVal     { return TAttrVal{Kind: 1, I: i} }
func TFloat(f float64) TAttrVal { return TAttrVal{Kind: 2, F: f} }
func TBool(b bool) TAttrVal     { return TAttrVal{Kind: 3, B: b} }

type TSpan struct {
	SpanID   [8]byte
	ParentID [8]byte // all zero = root span
	Name     string
	Kind     int // OTLP SpanKind: 0 unspecified, 1 internal, 2 server, 3 client, 4 producer, 5 consumer
	Status   int // OTLP StatusCode: 0 unset, 1 ok, 2 error
	StartNs  uint64
	DurNs    uint64
	Service  string              // resource service.name
	Resource map[string]TAttrVal // other resource attributes (dotted names)
	Attrs    map[string]TAttrVal // span attributes (dotted names)
}

type TTrace struct {
	TraceID [16]byte
	Spans   []*TSpan // any order; parents are found by SpanID
}
