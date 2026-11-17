//go:build opencoreamr && cgo

package amrnb

import "testing"

func BenchmarkRawAMRNBDecode20ms(b *testing.B) {
	d, err := NewDecoder()
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()
	source := frame(7)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Decode(source); err != nil {
			b.Fatal(err)
		}
	}
}
