package main

import (
	"fmt"
	"github.com/AtomiCloud/obs/internal/contract"
	"github.com/AtomiCloud/obs/internal/gen"
)

// generationTargetIssues is separate from discovery so every command shares the
// same target resolution while contract discovery can grow with repository layouts.
func generationTargetIssues(primary *contract.Contract, dir string) ([]string, error) {
	paths, err := contract.Discover(dir)
	if err != nil {
		return nil, err
	}
	contracts := []*contract.Contract{primary}
	for _, file := range paths {
		other, err := contract.Load(file)
		if err != nil {
			return nil, err
		}
		if issues := other.Validate(); len(issues) > 0 {
			return nil, fmt.Errorf("%s: %s", other.Rel(), issues[0])
		}
		contracts = append(contracts, other)
	}
	return gen.TargetClashes(contracts), nil
}
