package plugin

import (
	"math"
	"strconv"
	"time"
)

// MaxSafeInteger is the largest integer a JavaScript number holds exactly.
const MaxSafeInteger = 1<<53 - 1

// JSONValue makes a normalized value safe for JSON clients: exact numbers stay exact.
func JSONValue(v any) any {
	switch x := v.(type) {
	case int64:
		if x > MaxSafeInteger || x < -MaxSafeInteger {
			return strconv.FormatInt(x, 10)
		}
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return strconv.FormatFloat(x, 'g', -1, 64)
		}
		return x
	case Decimal:
		return string(x)
	case Date:
		return string(x)
	case TimeOfDay:
		return string(x)
	case JSON:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return v // string, bool, []byte (base64 in JSON), nil
}
