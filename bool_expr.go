package beindexer

type (
	BEField string

	Values interface{}

	Assignments map[BEField]Values

	BoolValues struct {
		Incl  bool   `json:"inc"`   // include: true exclude: false
		Value Values `json:"value"` // values can be parser parse to id
	}

	// expression: age (in) [15,16,17], city (not in) [shanghai,yz]
	BoolExprs struct {
		BoolValues
		Field BEField `json:"field"`
	}
)

func NewBoolExpr(field BEField, inc bool, v Values) *BoolExprs {
	expr := &BoolExprs{
		Field: field,
		BoolValues: BoolValues{
			Value: v,
			Incl:  inc,
		},
	}
	return expr
}
