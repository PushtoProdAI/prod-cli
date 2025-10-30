CREATE OR REPLACE FUNCTION public.get_deployment_operations(
  p_limit INTEGER DEFAULT 50,
  p_offset INTEGER DEFAULT 0
)
RETURNS TABLE (
  operation_type TEXT,
  resource_type TEXT,
  status TEXT,
  platform TEXT,
  language TEXT,
  started_at TIMESTAMP WITH TIME ZONE,
  completed_at TIMESTAMP WITH TIME ZONE,
  duration_seconds INTEGER
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN QUERY
  SELECT 
    d.operation_type,
    d.resource_type,
    d.status,
    d.platform,
    d.language,
    d.started_at,
    d.completed_at,
    d.duration_seconds
  FROM audit.deployment_operations d
  ORDER BY d.started_at DESC
  LIMIT p_limit
  OFFSET p_offset;
END;
$$;

CREATE OR REPLACE FUNCTION public.get_deployment_operations_count()
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN (SELECT COUNT(*) FROM audit.deployment_operations);
END;
$$;

GRANT EXECUTE ON FUNCTION public.get_deployment_operations(INTEGER, INTEGER) TO service_role;
GRANT EXECUTE ON FUNCTION public.get_deployment_operations_count() TO service_role;
