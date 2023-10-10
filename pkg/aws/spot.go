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

	var devpodSubnet string

	if providerAws.Config.VpcID != "" && providerAws.Config.SubnetID == "" {
		subnetID, err := GetSubnetID(ctx, providerAws)
		if err != nil {
			return nil, err
		}

		if subnetID == "" {
			return nil, fmt.Errorf("could not find a matching SubnetID in VPC %s, please specify one", providerAws.Config.VpcID)
		}
		devpodSubnet = subnetID
	}

	if providerAws.Config.SubnetID != "" {
		devpodSubnet = providerAws.Config.SubnetID
	}

	devpodSG, err := GetDevpodSecurityGroups(ctx, providerAws)
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
			InstanceType:     types.InstanceType(providerAws.Config.MachineType),
			SecurityGroupIds: devpodSG,
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
		// wait a second for the spot instance request to be available
		time.Sleep(1 * time.Second)
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

func DeleteSpot(ctx context.Context, cfg aws.Config, instanceID string) error {
	svc := ec2.NewFromConfig(cfg)

	describeReq := ec2.DescribeSpotInstanceRequestsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:devpod"),
				Values: []string{instanceID},
			},
		},
	}

	spotInstanceReqs, err := svc.DescribeSpotInstanceRequests(ctx, &describeReq)
	if err != nil {
		return err
	}

	cancelReq := ec2.CancelSpotInstanceRequestsInput{
		SpotInstanceRequestIds: []string{
			*spotInstanceReqs.SpotInstanceRequests[0].SpotInstanceRequestId,
		},
	}

	_, err = svc.CancelSpotInstanceRequests(ctx, &cancelReq)
	if err != nil {
		return err
	}

	terminateInstancesInput := &ec2.TerminateInstancesInput{
		InstanceIds: []string{
			instanceID,
		},
	}

	_, err = svc.TerminateInstances(ctx, terminateInstancesInput)
	if err != nil {
		return err
	}

	return err
}
