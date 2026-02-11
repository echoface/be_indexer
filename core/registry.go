package core

var (
	fieldBuilderFactory = make(map[string]FieldBuilderFactory)
	fieldIndexFactory   = make(map[string]FieldIndexFactory)
)

// RegisterField registers both builder and index factories for a field type.
// This is the recommended way to register field implementations.
func RegisterField(name string, impl FieldImplementation) {
	if impl.NewBuilder != nil {
		RegisterFieldBuilder(name, impl.NewBuilder)
	}
	if impl.NewIndex != nil {
		RegisterFieldIndex(name, impl.NewIndex)
	}
}

func NewFieldBuilder(name string) FieldIndexBuilder {
	if fn, ok := fieldBuilderFactory[name]; ok {
		return fn()
	}
	return nil
}

func NewFieldIndex(name string) FieldIndex {
	if fn, ok := fieldIndexFactory[name]; ok {
		return fn()
	}
	return nil
}

func HasFieldBuilder(name string) bool {
	_, ok := fieldBuilderFactory[name]
	return ok
}

func RegisterFieldBuilder(name string, builder FieldBuilderFactory) {
	// if HasFieldBuilder(name) {
	// 	 fmt.Printf("holder builder name:%s already registered, override!!!\n", name)
	// }
	fieldBuilderFactory[name] = builder
}

func RegisterFieldIndex(name string, builder FieldIndexFactory) {
	fieldIndexFactory[name] = builder
}
