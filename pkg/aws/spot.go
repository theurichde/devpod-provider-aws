package aws

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"time"
)

func CreateSpotInstance(ctx context.Context, cfg aws.Config, providerAws *AwsProvider) (*ec2.RequestSpotInstancesOutput, error) {
	svc := ec2.NewFromConfig(cfg)

	devpodSubnet, err := GetDevpodSubnet(ctx, providerAws)
	if err != nil {
		if err.Error() == "no devpod subnet found" {
			devpodSubnet, err = CreateDevpodSubnet(ctx, providerAws)
		}
	}

	devpodSG, err := GetDevpodSecurityGroup(ctx, providerAws)
	if err != nil {
		return nil, err
	}

	volSizeI32 := int32(providerAws.Config.DiskSizeGB)

	userData, err := GetInjectKeypairScript(providerAws.Config.MachineFolder)
	if err != nil {
		return nil, err
	}

	spotInstance := ec2.RequestSpotInstancesInput{
		InstanceCount: aws.Int32(1),
		Type:          types.SpotInstanceTypePersistent,
		LaunchSpecification: &types.RequestSpotLaunchSpecification{
			InstanceType: types.InstanceType(providerAws.Config.MachineType),
			SecurityGroupIds: []string{
				devpodSG,
			},
			BlockDeviceMappings: []types.BlockDeviceMapping{
				{
					DeviceName: aws.String("/dev/sda1"),
					Ebs: &types.EbsBlockDevice{
						VolumeSize: &volSizeI32,
					},
				},
			},
			ImageId:  aws.String(providerAws.Config.DiskImage),
			UserData: &userData,
			SubnetId: &devpodSubnet,
		},
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: "spot-instances-request",
				Tags: []types.Tag{
					{
						Key:   aws.String("devpod"),
						Value: aws.String(providerAws.Config.MachineID),
					},
				},
			},
		},
	}

	profile, err := GetDevpodInstanceProfile(ctx, providerAws)
	if err == nil {
		spotInstance.LaunchSpecification.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Arn: aws.String(profile),
		}
	}

	if providerAws.Config.SubnetID != "" {
		spotInstance.LaunchSpecification.SubnetId = &providerAws.Config.SubnetID
	}

	result, err := svc.RequestSpotInstances(ctx, &spotInstance)
	if err != nil {
		return nil, err
	}

	var instanceId string
	for {
		instanceRequests, err := svc.DescribeSpotInstanceRequests(ctx, &ec2.DescribeSpotInstanceRequestsInput{
			SpotInstanceRequestIds: []string{*result.SpotInstanceRequests[0].SpotInstanceRequestId},
		})
		if err != nil {
			return nil, err
		}

		if len(instanceRequests.SpotInstanceRequests) > 0 {
			if *instanceRequests.SpotInstanceRequests[0].Status.Code == "fulfilled" && instanceRequests.SpotInstanceRequests[0].InstanceId != nil {
				fmt.Printf("Spot instance fulfilled: %s\n", *instanceRequests.SpotInstanceRequests[0].InstanceId)
				instanceId = *instanceRequests.SpotInstanceRequests[0].InstanceId
				break
			}
		}
		fmt.Println("Waiting for spot instance fulfilment")
		time.Sleep(5 * time.Second)
	}

	_, err = svc.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{instanceId},
		Tags: []types.Tag{
			{
				Key:   aws.String("devpod"),
				Value: aws.String(providerAws.Config.MachineID),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return result, nil
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
				},
			},
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
