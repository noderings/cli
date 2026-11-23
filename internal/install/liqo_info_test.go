package install

import "testing"

func TestLiqoInfoIndicatesReadyRequiresClusterID(t *testing.T) {
	empty := `{"health":{"healthy":true},"local":{"clusterID":"","version":""},"peerings":{"peers":[]}}`
	if liqoInfoIndicatesReady(empty) {
		t.Fatal("empty clusterID must not count as installed")
	}
	ok := `{"health":{"healthy":true},"local":{"clusterID":"abc-123","version":"v1"},"peerings":{"peers":[]}}`
	if !liqoInfoIndicatesReady(ok) {
		t.Fatal("healthy install with clusterID should be ready")
	}
}
