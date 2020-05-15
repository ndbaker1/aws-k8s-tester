package eksconfig

import (
	"fmt"
	"math"
	"testing"

	"github.com/aws/aws-k8s-tester/pkg/metrics"
)

func TestRequestsSummary(t *testing.T) {
	rs := RequestsSummary{
		SuccessTotal: 10,
		FailureTotal: 10,
		LatencyHistogram: metrics.HistogramBuckets([]metrics.HistogramBucket{
			{Scale: "milliseconds", LowerBound: 0, UpperBound: 0.5, Count: 0},
			{Scale: "milliseconds", LowerBound: 0.5, UpperBound: 1, Count: 2},
			{Scale: "milliseconds", LowerBound: 1, UpperBound: 2, Count: 0},
			{Scale: "milliseconds", LowerBound: 2, UpperBound: 4, Count: 0},
			{Scale: "milliseconds", LowerBound: 4, UpperBound: 8, Count: 0},
			{Scale: "milliseconds", LowerBound: 8, UpperBound: 16, Count: 8},
			{Scale: "milliseconds", LowerBound: 16, UpperBound: 32, Count: 0},
			{Scale: "milliseconds", LowerBound: 32, UpperBound: 64, Count: 100},
			{Scale: "milliseconds", LowerBound: 64, UpperBound: 128, Count: 0},
			{Scale: "milliseconds", LowerBound: 128, UpperBound: 256, Count: 0},
			{Scale: "milliseconds", LowerBound: 256, UpperBound: 512, Count: 20},
			{Scale: "milliseconds", LowerBound: 512, UpperBound: 1024, Count: 0},
			{Scale: "milliseconds", LowerBound: 1024, UpperBound: 2048, Count: 0},
			{Scale: "milliseconds", LowerBound: 2048, UpperBound: 4096, Count: 0},
			{Scale: "milliseconds", LowerBound: 4096, UpperBound: math.MaxFloat64, Count: 4},
		}),
	}
	fmt.Println(rs.JSON())
	fmt.Println(rs.Table())
}
