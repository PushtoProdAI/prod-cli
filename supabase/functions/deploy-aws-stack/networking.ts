// VPC networking resources for AWS deployments

/**
 * Build VPC networking resources (VPC, subnets, security groups, internet gateway, route tables)
 * This is conditionally created based on whether backing services or migrations are needed
 */
export function buildNetworkingResources(
  serviceName: string,
  tenantId: string,
  hasMigrations: boolean,
  hasRds: boolean,
  resources: any
): void {
  // VPC
  resources.VPC = {
    Type: 'AWS::EC2::VPC',
    Properties: {
      CidrBlock: '10.0.0.0/16',
      EnableDnsHostnames: true,
      EnableDnsSupport: true,
      Tags: [
        { Key: 'Name', Value: `prod-${serviceName}-vpc` },
        { Key: 'tenant', Value: tenantId },
      ],
    },
  };

  // Subnets - Private subnets for RDS and other backing services
  resources.PrivateSubnetAZ1 = {
    Type: 'AWS::EC2::Subnet',
    Properties: {
      VpcId: { Ref: 'VPC' },
      CidrBlock: '10.0.1.0/24',
      AvailabilityZone: { 'Fn::Select': [0, { 'Fn::GetAZs': '' }] },
      Tags: [
        { Key: 'Name', Value: `prod-${serviceName}-private-az1` },
        { Key: 'Type', Value: 'Private' },
      ],
    },
  };

  resources.PrivateSubnetAZ2 = {
    Type: 'AWS::EC2::Subnet',
    Properties: {
      VpcId: { Ref: 'VPC' },
      CidrBlock: '10.0.2.0/24',
      AvailabilityZone: { 'Fn::Select': [1, { 'Fn::GetAZs': '' }] },
      Tags: [
        { Key: 'Name', Value: `prod-${serviceName}-private-az2` },
        { Key: 'Type', Value: 'Private' },
      ],
    },
  };

  // Public subnets for ECS tasks (need internet access to pull images from ECR)
  // Only create these if migrations are present
  if (hasMigrations) {
    resources.PublicSubnetAZ1 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.10.0/24',
        AvailabilityZone: { 'Fn::Select': [0, { 'Fn::GetAZs': '' }] },
        MapPublicIpOnLaunch: true,
        Tags: [
          { Key: 'Name', Value: `prod-${serviceName}-public-az1` },
          { Key: 'Type', Value: 'Public' },
        ],
      },
    };

    resources.PublicSubnetAZ2 = {
      Type: 'AWS::EC2::Subnet',
      Properties: {
        VpcId: { Ref: 'VPC' },
        CidrBlock: '10.0.11.0/24',
        AvailabilityZone: { 'Fn::Select': [1, { 'Fn::GetAZs': '' }] },
        MapPublicIpOnLaunch: true,
        Tags: [
          { Key: 'Name', Value: `prod-${serviceName}-public-az2` },
          { Key: 'Type', Value: 'Public' },
        ],
      },
    };

    // Internet Gateway for public subnets
    resources.InternetGateway = {
      Type: 'AWS::EC2::InternetGateway',
      Properties: {
        Tags: [
          { Key: 'Name', Value: `prod-${serviceName}-igw` },
        ],
      },
    };

    resources.AttachGateway = {
      Type: 'AWS::EC2::VPCGatewayAttachment',
      Properties: {
        VpcId: { Ref: 'VPC' },
        InternetGatewayId: { Ref: 'InternetGateway' },
      },
    };

    // Route table for public subnets
    resources.PublicRouteTable = {
      Type: 'AWS::EC2::RouteTable',
      Properties: {
        VpcId: { Ref: 'VPC' },
        Tags: [
          { Key: 'Name', Value: `prod-${serviceName}-public-rt` },
        ],
      },
    };

    resources.PublicRoute = {
      Type: 'AWS::EC2::Route',
      DependsOn: 'AttachGateway',
      Properties: {
        RouteTableId: { Ref: 'PublicRouteTable' },
        DestinationCidrBlock: '0.0.0.0/0',
        GatewayId: { Ref: 'InternetGateway' },
      },
    };

    resources.PublicSubnetRouteTableAssociationAZ1 = {
      Type: 'AWS::EC2::SubnetRouteTableAssociation',
      Properties: {
        SubnetId: { Ref: 'PublicSubnetAZ1' },
        RouteTableId: { Ref: 'PublicRouteTable' },
      },
    };

    resources.PublicSubnetRouteTableAssociationAZ2 = {
      Type: 'AWS::EC2::SubnetRouteTableAssociation',
      Properties: {
        SubnetId: { Ref: 'PublicSubnetAZ2' },
        RouteTableId: { Ref: 'PublicRouteTable' },
      },
    };
  }

  // Security Group for backing services
  resources.BackingServiceSecurityGroup = {
    Type: 'AWS::EC2::SecurityGroup',
    Properties: {
      GroupDescription: 'Security group for backing services',
      VpcId: { Ref: 'VPC' },
      SecurityGroupIngress: [
        {
          IpProtocol: 'tcp',
          FromPort: 5432,
          ToPort: 5432,
          SourceSecurityGroupId: { Ref: 'AppRunnerSecurityGroup' },
        },
        {
          IpProtocol: 'tcp',
          FromPort: 6379,
          ToPort: 6379,
          SourceSecurityGroupId: { Ref: 'AppRunnerSecurityGroup' },
        },
      ],
      Tags: [{ Key: 'Name', Value: `prod-${serviceName}-backing-sg` }],
    },
  };

  // Security Group for App Runner
  resources.AppRunnerSecurityGroup = {
    Type: 'AWS::EC2::SecurityGroup',
    Properties: {
      GroupDescription: 'Security group for App Runner',
      VpcId: { Ref: 'VPC' },
      Tags: [{ Key: 'Name', Value: `prod-${serviceName}-apprunner-sg` }],
    },
  };

  // DB Subnet Group (if RDS is needed)
  if (hasRds) {
    resources.DBSubnetGroup = {
      Type: 'AWS::RDS::DBSubnetGroup',
      Properties: {
        DBSubnetGroupDescription: 'Subnet group for RDS',
        SubnetIds: [{ Ref: 'PrivateSubnetAZ1' }, { Ref: 'PrivateSubnetAZ2' }],
        Tags: [{ Key: 'Name', Value: `prod-${serviceName}-db-subnet` }],
      },
    };
  }
}
