package inference

func (m LlmMessage) NameValue() string {
	if m.Name != nil && *m.Name != "" {
		return *m.Name
	}
	return "tool"
}
