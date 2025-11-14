/**
 * Standard tags for all Prod-managed AWS resources
 * SECURITY: These tags are used in IAM resource-based access policies
 * to ensure operations are scoped to Prod-managed resources only
 */
export function getStandardTags(tenantId: string, serviceName: string) {
  return [
    { Key: 'ManagedBy', Value: 'Prod' },
    { Key: 'tenant', Value: tenantId },
    { Key: 'service', Value: serviceName },
  ];
}
