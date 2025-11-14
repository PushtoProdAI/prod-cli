-- Add function to get deployment history with metadata for rollback support
CREATE OR REPLACE FUNCTION public.get_deployment_history(
  p_user_id UUID,
  p_resource_name TEXT,
  p_platform TEXT,
  p_limit INTEGER DEFAULT 10
)
RETURNS TABLE (
  operation_id UUID,
  operation_type TEXT,
  resource_type TEXT,
  resource_id TEXT,
  resource_name TEXT,
  status TEXT,
  platform TEXT,
  language TEXT,
  started_at TIMESTAMP WITH TIME ZONE,
  completed_at TIMESTAMP WITH TIME ZONE,
  metadata JSONB
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN QUERY
  SELECT 
    d.id as operation_id,
    d.operation_type,
    d.resource_type,
    d.resource_id,
    d.resource_name,
    d.status,
    d.platform,
    d.language,
    d.started_at,
    d.completed_at,
    d.metadata
  FROM audit.deployment_operations d
  WHERE d.user_id = p_user_id
    AND d.resource_name = p_resource_name
    AND d.platform = p_platform
    AND d.status = 'success'
    AND d.operation_type = 'deploy'
  ORDER BY d.completed_at DESC NULLS LAST
  LIMIT p_limit;
END;
$$;

-- Grant execute permissions
GRANT EXECUTE ON FUNCTION public.get_deployment_history(UUID, TEXT, TEXT, INTEGER) TO authenticated;
GRANT EXECUTE ON FUNCTION public.get_deployment_history(UUID, TEXT, TEXT, INTEGER) TO service_role;
