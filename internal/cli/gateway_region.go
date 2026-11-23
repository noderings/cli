package cli

import (
	"fmt"

	generated "github.com/noderings/cli/internal/api/generated"
)

func parseAgentGatewayRegion(gatewayRegion string) (generated.V1AgentGatewayRegion, error) {
	switch gatewayRegion {
	case "AMS01", "ams01", "AGENT_GATEWAY_REGION_AMS01":
		return generated.V1AgentGatewayRegionAGENTGATEWAYREGIONAMS01, nil
	default:
		return "", fmt.Errorf("invalid gateway region: %s (valid: AMS01)", gatewayRegion)
	}
}
