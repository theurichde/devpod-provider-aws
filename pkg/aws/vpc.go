package aws

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"strings"
)

// GetDevpodVPC retrieves the VPC ID for the devpod VPC.
// If it doesn't exist, we check if we want to create a dedicated VPC, otherwise it tries to take the default VPC
func GetDevpodVPC(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.VpcID != "" {
		return provider.Config.VpcID, nil
	}

	// Get a list of VPCs, so we can associate the group with the first VPC.
	svc := ec2.NewFromConfig(provider.AwsConfig)
	result, err := svc.DescribeVpcs(ctx, nil)
	if err != nil {
		return "", err
	}

	if len(result.Vpcs) == 0 {
		return "", errors.New("there are no VPCs to associate with")
	}

	for _, vpc := range result.Vpcs {
		for _, tag := range vpc.Tags {
			if *tag.Key == "Name" && strings.Contains(*tag.Value, "devpod") {
				return *vpc.VpcId, nil
			}
		}
	}

	if provider.Config.CreateVpc {
		vpc, err := CreateDevpodVpc(ctx, provider)
		if err != nil {
			return "", err
		}
		return vpc, nil
	}

	// No dedicated devpod VPC found, so we need to find a default vpc
	for _, vpc := range result.Vpcs {
		if *vpc.IsDefault {
			return *vpc.VpcId, nil
		}
	}

	return "", errors.New("no suitable VPC found")
}

// CreateDevpodVpc creates a new VPC for devpod
func CreateDevpodVpc(ctx context.Context, provider *AwsProvider) (string, error) {
	svc := ec2.NewFromConfig(provider.AwsConfig)
	input := &ec2.CreateVpcInput{
		CidrBlock:                   aws.String("10.0.0.0/16"),
		AmazonProvidedIpv6CidrBlock: aws.Bool(true),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpc,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String(fmt.Sprintf("devpod")),
					},
				},
			},
		},
	}
	vpc, err := svc.CreateVpc(ctx, input)
	if err != nil {
		return "", err
	}
	return *vpc.Vpc.VpcId, nil
}

func GetDevpodSecurityGroup(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.SecurityGroupID != "" {
		return provider.Config.SecurityGroupID, nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)
	input := &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					"devpod",
				},
			},
		},
	}

	if provider.Config.VpcID != "" {
		input.Filters = append(input.Filters, types.Filter{
			Name: aws.String("vpc-id"),
			Values: []string{
				provider.Config.VpcID,
			},
		})
	}

	result, err := svc.DescribeSecurityGroups(ctx, input)
	// If it is not created, do it
	if len(result.SecurityGroups) == 0 || err != nil {
		return CreateDevpodSecurityGroup(ctx, provider)
	}

	return *result.SecurityGroups[0].GroupId, nil
}

func CreateDevpodSecurityGroup(ctx context.Context, provider *AwsProvider) (string, error) {
	var err error

	svc := ec2.NewFromConfig(provider.AwsConfig)
	vpc, err := GetDevpodVPC(ctx, provider)
	if err != nil {
		return "", err
	}

	// Create the security group with the VPC, name, and description.
	result, err := svc.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("devpod"),
		Description: aws.String("Default Security Group for DevPod"),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group",
				Tags: []types.Tag{
					{
						Key:   aws.String("devpod"),
						Value: aws.String("devpod"),
					},
				},
			},
		},
		VpcId: aws.String(vpc),
	})
	if err != nil {
		return "", err
	}

	groupID := *result.GroupId

	// Add permissions to the security group
	_, err = svc.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(groupID),
		IpPermissions: []types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(22),
				ToPort:     aws.Int32(22),
				IpRanges: []types.IpRange{
					{
						CidrIp: aws.String("0.0.0.0/0"),
					},
				},
			},
		},
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "security-group-rule",
				Tags: []types.Tag{
					{
						Key:   aws.String("devpod"),
						Value: aws.String("devpod-ingress"),
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}

	return groupID, nil
}

// TODO Route Table Internet Gateway and Subnet Association needed
func CreateDevpodSubnet(ctx context.Context, providerAws *AwsProvider) (string, error) {
	svc := ec2.NewFromConfig(providerAws.AwsConfig)

	vpc, err := GetDevpodVPC(ctx, providerAws)
	if err != nil {
		return "", err
	}

	subnet, err := svc.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		CidrBlock: aws.String("10.0.0.0/24"),
		VpcId:     aws.String(vpc),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeSubnet,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("devpod"),
					},
					{
						Key:   aws.String("devpod"),
						Value: aws.String(providerAws.Config.MachineID),
					},
				},
			},
		},
	})

	if err != nil {
		return "", err
	}

	_, err = svc.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
		SubnetId: subnet.Subnet.SubnetId,
		MapPublicIpOnLaunch: &types.AttributeBooleanValue{
			Value: aws.Bool(true),
		},
	})
	if err != nil {
		return "", err
	}

	routeTable, err := svc.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
		VpcId: aws.String(vpc),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeRouteTable,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("devpod"),
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}

	_, err = svc.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
		SubnetId:     subnet.Subnet.SubnetId,
		RouteTableId: routeTable.RouteTable.RouteTableId,
	})
	if err != nil {
		return "", err
	}

	_, err = svc.CreateRoute(ctx, &ec2.CreateRouteInput{
		DestinationCidrBlock: subnet.Subnet.CidrBlock,
		RouteTableId:         routeTable.RouteTable.RouteTableId,
	})
	if err != nil {
		return "", err
	}

	return *subnet.Subnet.SubnetId, nil
}

func GetSubnetID(ctx context.Context, provider *AwsProvider) (string, error) {
	if provider.Config.SubnetID != "" {
		return provider.Config.SubnetID, nil
	}

	svc := ec2.NewFromConfig(provider.AwsConfig)

	// first search for a default devpod specific subnet, if it fails
	// we search the subnet with most free IPs that can do also public-ipv4
	input := &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("tag:devpod"),
				Values: []string{
					"devpod",
				},
			},
		},
	}

	result, err := svc.DescribeSubnets(ctx, input)
	if err != nil {
		return "", err
	}

	if len(result.Subnets) > 0 {
		return *result.Subnets[0].SubnetId, nil
	}

	input = &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name: aws.String("vpc-id"),
				Values: []string{
					provider.Config.VpcID,
				},
			},
			{
				Name: aws.String("map-public-ip-on-launch"),
				Values: []string{
					"true",
				},
			},
		},
	}

	result, err = svc.DescribeSubnets(ctx, input)
	if err != nil {
		return "", err
	}

	var maxIPCount int32

	subnetID := ""

	for _, v := range result.Subnets {
		if *v.AvailableIpAddressCount > maxIPCount {
			maxIPCount = *v.AvailableIpAddressCount
			subnetID = *v.SubnetId
		}
	}

	return subnetID, nil
}

func GetDevpodSubnet(ctx context.Context, providerAws *AwsProvider) (string, error) {
	svc := ec2.NewFromConfig(providerAws.AwsConfig)

	subnets, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: []string{"devpod"},
			},
			// TODO filter for machineId specific subnet(s)?
		},
	})
	if err != nil {
		return "", err
	}

	if len(subnets.Subnets) == 0 {
		return "", fmt.Errorf("no devpod subnet found")
	}

	return *subnets.Subnets[0].SubnetId, nil

}
