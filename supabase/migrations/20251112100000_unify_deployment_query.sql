-- Unified function to query deployment operations with optional user scoping
-- Replaces both get_deployment_operations and get_deployment_history
CREATE OR REPLACE FUNCTION public.query_deployment_operations(
  p_user_id UUID DEFAULT NULL,          -- If NULL and user is admin, returns all users
  p_resource_name TEXT DEFAULT NULL,    -- Optional filter by service name
  p_platform TEXT DEFAULT NULL,         -- Optional filter by platform
  p_status TEXT DEFAULT NULL,           -- Optional filter by status
  p_operation_type TEXT DEFAULT NULL,   -- Optional filter by operation type
  p_limit INTEGER DEFAULT 50,
  p_offset INTEGER DEFAULT 0
)
RETURNS TABLE (
  operation_id UUID,
  user_id UUID,
  operation_type TEXT,
  resource_type TEXT,
  resource_id TEXT,
  resource_name TEXT,
  status TEXT,
  platform TEXT,
  language TEXT,
  started_at TIMESTAMP WITH TIME ZONE,
  completed_at TIMESTAMP WITH TIME ZONE,
  duration_seconds INTEGER,
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
    d.user_id,
    d.operation_type,
    d.resource_type,
    d.resource_id,
    d.resource_name,
    d.status,
    d.platform,
    d.language,
    d.started_at,
    d.completed_at,
    d.duration_seconds,
    d.metadata
  FROM audit.deployment_operations d
  WHERE (p_user_id IS NULL OR d.user_id = p_user_id)
    AND (p_resource_name IS NULL OR d.resource_name = p_resource_name)
    AND (p_platform IS NULL OR d.platform = p_platform)
    AND (p_status IS NULL OR d.status = p_status)
    AND (p_operation_type IS NULL OR d.operation_type = p_operation_type)
  ORDER BY d.completed_at DESC NULLS LAST, d.started_at DESC
  LIMIT p_limit
  OFFSET p_offset;
END;
$$;

-- Count function with same filtering capabilities
CREATE OR REPLACE FUNCTION public.count_deployment_operations(
  p_user_id UUID DEFAULT NULL,
  p_resource_name TEXT DEFAULT NULL,
  p_platform TEXT DEFAULT NULL,
  p_status TEXT DEFAULT NULL,
  p_operation_type TEXT DEFAULT NULL
)
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN (
    SELECT COUNT(*)
    FROM audit.deployment_operations d
    WHERE (p_user_id IS NULL OR d.user_id = p_user_id)
      AND (p_resource_name IS NULL OR d.resource_name = p_resource_name)
      AND (p_platform IS NULL OR d.platform = p_platform)
      AND (p_status IS NULL OR d.status = p_status)
      AND (p_operation_type IS NULL OR d.operation_type = p_operation_type)
  );
END;
$$;

-- Grant execute permissions
GRANT EXECUTE ON FUNCTION public.query_deployment_operations(UUID, TEXT, TEXT, TEXT, TEXT, INTEGER, INTEGER) TO authenticated;
GRANT EXECUTE ON FUNCTION public.query_deployment_operations(UUID, TEXT, TEXT, TEXT, TEXT, INTEGER, INTEGER) TO service_role;
GRANT EXECUTE ON FUNCTION public.count_deployment_operations(UUID, TEXT, TEXT, TEXT, TEXT) TO authenticated;
GRANT EXECUTE ON FUNCTION public.count_deployment_operations(UUID, TEXT, TEXT, TEXT, TEXT) TO service_role;
