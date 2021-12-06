package bitsinvt

var (
	containerBuilderFactory map[string]ContainerBuilder
)

type (
	ContainerBuilder func() BEContainer
)

func init() {
	containerBuilderFactory = make(map[string]ContainerBuilder)
	containerBuilderFactory["common"] = func() BEContainer {
		return NewCommonBEContainer()
	}
	containerBuilderFactory["bsi"] = func() BEContainer {
		return NewBSIBEContainer()
	}
}
