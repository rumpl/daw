package sessionlineage

const (
	AttributeParentSessionID = "daw.origin.parent_session_id"
	AttributeRootSessionID   = "daw.origin.root_session_id"
	AttributeKind            = "daw.origin.kind"
	AttributePluginID        = "daw.origin.plugin_id"
)

const KindAgent = "agent"

type Origin struct {
	ParentSessionID string
	RootSessionID   string
	Kind            string
	PluginID        string
}

func FromAttributes(attributes map[string]string) Origin {
	return Origin{
		ParentSessionID: attributes[AttributeParentSessionID],
		RootSessionID:   attributes[AttributeRootSessionID],
		Kind:            attributes[AttributeKind],
		PluginID:        attributes[AttributePluginID],
	}
}

func (o Origin) Attributes() map[string]string {
	attributes := map[string]string{}
	if o.ParentSessionID != "" {
		attributes[AttributeParentSessionID] = o.ParentSessionID
	}
	if o.RootSessionID != "" {
		attributes[AttributeRootSessionID] = o.RootSessionID
	}
	if o.Kind != "" {
		attributes[AttributeKind] = o.Kind
	}
	if o.PluginID != "" {
		attributes[AttributePluginID] = o.PluginID
	}
	return attributes
}

func Merge(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
