/*
Copyright 2020 Cortex Labs, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"math"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/cortexlabs/cortex/pkg/lib/errors"
	"github.com/cortexlabs/cortex/pkg/lib/parallel"
	"github.com/cortexlabs/cortex/pkg/lib/sets/strset"
	s "github.com/cortexlabs/cortex/pkg/lib/strings"
)

func (c *Client) SpotInstancePrice(region string, instanceType string) (float64, error) {
	result, err := c.EC2().DescribeSpotPriceHistory(&ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       []*string{aws.String(instanceType)},
		ProductDescriptions: []*string{aws.String("Linux/UNIX")},
		StartTime:           aws.Time(time.Now()),
	})
	if err != nil {
		return 0, errors.Wrap(err, "checking spot instance price")
	}

	min := math.MaxFloat64

	for _, spotPrice := range result.SpotPriceHistory {
		if spotPrice == nil {
			continue
		}

		price, ok := s.ParseFloat64(*spotPrice.SpotPrice)
		if !ok {
			continue
		}

		if price < min {
			min = price
		}
	}

	if min == math.MaxFloat64 {
		return 0, ErrorNoValidSpotPrices(instanceType, region)
	}

	if min <= 0 {
		return 0, ErrorNoValidSpotPrices(instanceType, region)
	}

	return min, nil
}

func (c *Client) ListAvailabilityZones() (strset.Set, error) {
	input := &ec2.DescribeAvailabilityZonesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("region-name"),
				Values: []*string{aws.String(c.Region)},
			},
			{
				Name:   aws.String("state"),
				Values: []*string{aws.String(ec2.AvailabilityZoneStateAvailable)},
			},
		},
	}

	result, err := c.EC2().DescribeAvailabilityZones(input)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	zones := strset.New()
	for _, az := range result.AvailabilityZones {
		if az.ZoneName != nil {
			zones.Add(*az.ZoneName)
		}
	}

	return zones, nil
}

func (c *Client) listSupportedAvailabilityZonesSingle(instanceType string) (strset.Set, error) {
	input := &ec2.DescribeReservedInstancesOfferingsInput{
		InstanceType:       &instanceType,
		IncludeMarketplace: aws.Bool(false),
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("scope"),
				Values: []*string{aws.String(ec2.ScopeAvailabilityZone)},
			},
		},
	}

	zones := strset.New()
	err := c.EC2().DescribeReservedInstancesOfferingsPages(input, func(output *ec2.DescribeReservedInstancesOfferingsOutput, lastPage bool) bool {
		for _, offering := range output.ReservedInstancesOfferings {
			if offering.AvailabilityZone != nil {
				zones.Add(*offering.AvailabilityZone)
			}
		}
		return true
	})

	if err != nil {
		return nil, errors.WithStack(err)
	}

	return zones, nil
}

func (c *Client) ListSupportedAvailabilityZones(instanceType string, instanceTypes ...string) (strset.Set, error) {
	allInstanceTypes := append(instanceTypes, instanceType)
	zoneSets := make([]strset.Set, len(allInstanceTypes))
	fns := make([]func() error, len(allInstanceTypes))

	for i := range allInstanceTypes {
		localIdx := i
		fns[i] = func() error {
			zones, err := c.listSupportedAvailabilityZonesSingle(allInstanceTypes[localIdx])
			if err != nil {
				return err
			}
			zoneSets[localIdx] = zones
			return nil
		}
	}

	err := parallel.RunFirstErr(fns[0], fns[1:]...)
	if err != nil {
		return nil, err
	}

	return strset.Intersection(zoneSets...), nil
}
