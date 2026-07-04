package chunk

// gear is the 256-entry gear-hash table. Values are generated
// deterministically (splitmix64 from a fixed seed) so chunk boundaries are
// reproducible across processes and versions — a hard requirement for
// cross-version reuse. A per-tenant randomized gear table (anti-
// fingerprinting) is deferred to the E2EE era (Kamara SPEC §9).
var gear = func() [256]uint64 {
	var t [256]uint64
	x := uint64(0x9e3779b97f4a7c15) // fixed seed
	for i := range t {
		// splitmix64
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		t[i] = z
	}
	return t
}()
