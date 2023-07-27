package aws

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"io"
	"net/http"
	"strings"
)

// GetDevpodVPC retrieves the VPC ID for the devpod VPC.
// If it doesn't exist, it takes the default VPC, otherwise it creates a dedicated devpod VPC
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

	// No dedicated devpod VPC found, so we need to find a default vpc
	for _, vpc := range result.Vpcs {
		if *vpc.IsDefault {
			return *vpc.VpcId, nil
		}
	}

	// TODO introduce option for creating a dedicated VPC
	// No default VPC found, so we need to create one
	vpc, err := CreateDevpodVpc(ctx, provider)
	if err != nil {
		return "", err
	}

	return vpc, nil
}

// CreateDevpodVpc creates a new VPC for devpod dedicated to the machine ID
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
						Value: aws.String(fmt.Sprintf("devpod-%s", provider.Config.MachineID)),
					},
					{
						Key:   aws.String("devpod"),
						Value: aws.String(provider.Config.MachineID),
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

	result, err := svc.DescribeSecurityGroups(ctx, input)
	// If it is not created, do it
	if len(result.SecurityGroups) == 0 || err != nil {
		return CreateDevpodSecurityGroup(ctx, provider)
	}

	return *result.SecurityGroups[0].GroupId, nil
}

// CreateDevpodSecurityGroup creates a new (paranoid) security group for devpod dedicated
// to the machine ID and opens port 22 to the current IP
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
						Value: aws.String(provider.Config.MachineID),
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

	ownIp, err := getLocalIP()
	if err != nil {
		return "", err
	}

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
						CidrIp: aws.String(ownIp),
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

func getLocalIP() (string, error) {
	resp, err := http.Get("https://checkip.amazonaws.com")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	ownIp := string(bodyBytes)
	ownIp = strings.TrimSpace(ownIp)
	ownIp = strings.ReplaceAll(ownIp, "\n", "")
	ownIp = ownIp + "/32"

	return ownIp, nil
}

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

	return *subnet.Subnet.SubnetId, nil
}

func GetDevpodSubnet(ctx context.Context, providerAws *AwsProvider) (string, error) {
	svc := ec2.NewFromConfig(providerAws.AwsConfig)

	subnets, err := svc.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: []string{"devpod"},
			},
			// TODO filter for machineId specific subnet(s)
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
