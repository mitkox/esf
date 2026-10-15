package cube

import (
	"context"
	"fmt"
	"github.com/mitkox/esf/internal/sandbox"
)

// ValidateResources checks the actual template-driven VM allocation. Cube's
// create adapter does not accept per-request CPU/memory overrides.
func (p *Provider) ValidateResources(ctx context.Context, id string, expected sandbox.Limits) error {
	vm, err := p.client.Connect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect resource allocation: %w", classify(err))
	}
	info, err := vm.GetInfo(ctx)
	if err != nil {
		return fmt.Errorf("inspect resource allocation: %w", classify(err))
	}
	cpu := info.CPUCount * 1000
	if info.CPUMilli != nil {
		cpu = *info.CPUMilli
	}
	if cpu != expected.CPUMilli || info.MemoryMB != expected.MemoryMiB {
		return fmt.Errorf("template resources differ: actual cpu_milli=%d memory_mib=%d; expected cpu_milli=%d memory_mib=%d", cpu, info.MemoryMB, expected.CPUMilli, expected.MemoryMiB)
	}
	return nil
}
