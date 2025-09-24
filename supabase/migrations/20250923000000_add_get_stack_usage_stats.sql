CREATE OR REPLACE FUNCTION public.get_stack_usage_stats(
  p_platform TEXT DEFAULT NULL,
  p_language TEXT DEFAULT NULL,
  p_service_type TEXT DEFAULT NULL,
  p_service_provider TEXT DEFAULT NULL
)
RETURNS TABLE (
  platform TEXT,
  language TEXT,
  service_type TEXT,
  service_provider TEXT,
  usage_count BIGINT,
  last_used TIMESTAMP WITH TIME ZONE
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN QUERY
  SELECT s.platform, s.language, s.service_type, s.service_provider, s.usage_count, s.last_used
  FROM internal.requested_stack_usage_stats s
  WHERE (p_platform IS NULL OR s.platform = p_platform)
    AND (p_language IS NULL OR s.language = p_language)
    AND (p_service_type IS NULL OR s.service_type = p_service_type)
    AND (p_service_provider IS NULL OR s.service_provider = p_service_provider)
  ORDER BY s.usage_count DESC, s.last_used DESC;
END;
$$;

REVOKE EXECUTE ON FUNCTION public.get_stack_usage_stats(TEXT, TEXT, TEXT, TEXT) FROM anon;
REVOKE EXECUTE ON FUNCTION public.get_stack_usage_stats(TEXT, TEXT, TEXT, TEXT) FROM authenticated;
REVOKE EXECUTE ON FUNCTION public.get_stack_usage_stats(TEXT, TEXT, TEXT, TEXT) FROM public;

GRANT EXECUTE ON FUNCTION public.get_stack_usage_stats(TEXT, TEXT, TEXT, TEXT) TO service_role;