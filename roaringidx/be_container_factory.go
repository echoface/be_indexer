package roaringidx

var containerFactory map[string]ContainerBuilderFunc

type (
	ContainerBuilderFunc func(meta *FieldMeta) BEContainerBuilder
)

const (
	ContainerNameDefault = "default"
	ContainerNameAcMatch = "ac_matcher"
)

func init() {
	containerFactory = make(map[string]ContainerBuilderFunc)

	containerFactory[ContainerNameDefault] = func(meta *FieldMeta) BEContainerBuilder {
		return NewDefaultBEContainerBuilder(meta)
	}
	containerFactory[ContainerNameAcMatch] = func(meta *FieldMeta) BEContainerBuilder {
		return NewACBEContainerBuilder(meta)
	}
	//containerFactory["bsi"] = func(setting FieldSetting) BEContainerBuilder {
	//	return NewBSIBEContainerBuilder(setting)
	//}
}

func RegisterContainerBuilder(name string, builderFunc ContainerBuilderFunc) bool {
	if _, ok := containerFactory[name]; ok {
		return false
	}
	containerFactory[name] = builderFunc
	return true
}

func NewContainerBuilder(meta *FieldMeta) BEContainerBuilder {
	if fn, ok := containerFactory[meta.Container]; ok {
		return fn(meta)
	}
	return nil
}
