// Package capability describes local SDK features and parameter constraints.
// It never probes a remote service.
package capability

// Status describes known SDK/model support, not account entitlement or a
// guarantee that a particular voice or remote service accepts the request.
type Status string

const (
	Unknown     Status = "unknown"
	Supported   Status = "supported"
	Unsupported Status = "unsupported"
)

// Kind describes the Go option value, before native wire serialization.
type Kind string

const (
	Bool    Kind = "bool"
	Integer Kind = "integer"
	Number  Kind = "number"
	String  Kind = "string"
	Object  Kind = "object"
	List    Kind = "list"
)

// NumericRange is an inclusive interval in the stated unit.
type NumericRange struct {
	Min float64
	Max float64
}

// Parameter identifies a request field and known local constraints. Field
// is relative to asr.Config or tts.Request/DuplexRequest. Provider denotes
// the concrete value inside Options.Provider. Nil Range and empty enum
// slices mean no constraint is described, not unrestricted support.
type Parameter struct {
	Field        string
	Kind         Kind
	Unit         string
	Range        *NumericRange
	IntValues    []int
	StringValues []string
}

// Feature describes one advanced capability. Notes explain model, mode,
// dependency and output restrictions that cannot be reduced to a range.
type Feature struct {
	Status     Status
	Parameters []Parameter
	Notes      string
}

// Features is a snapshot owned by the caller. An absent key is Unknown,
// not Unsupported. Each Capabilities call returns independent data.
type Features map[string]Feature

func (f Features) Get(name string) Feature {
	if v, ok := f[name]; ok {
		return v
	}
	return Feature{Status: Unknown}
}

func (f Features) Supports(name string) bool {
	return f.Get(name).Status == Supported
}

// FromBool converts a definitive local support predicate to a status.
func FromBool(ok bool) Status {
	if ok {
		return Supported
	}
	return Unsupported
}
