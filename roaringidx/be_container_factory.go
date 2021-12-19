package roaringidx

var containerFactory map[string]ContainerBuilderFunc

type (
	ContainerBuilderFunc func(setting FieldSetting) BEContainerBuilder
)

const (
	ContainerNameDefault = "default"
	ContainerNameAcMatch = "ac_matcher"
)

func init() {
	containerFactory = make(map[string]ContainerBuilderFunc)

	containerFactory[ContainerNameDefault] = func(setting FieldSetting) BEContainerBuilder {
		return NewDefaultBEContainerBuilder(setting)
	}
	containerFactory[ContainerNameAcMatch] = func(setting FieldSetting) BEContainerBuilder {
		return NewACBEContainerBuilder(setting)
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

func NewContainerBuilder(name string, setting FieldSetting) BEContainerBuilder {
	if fn, ok := containerFactory[name]; ok {
		return fn(setting)
	}
	return nil
}
