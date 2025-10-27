REVOKE EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) FROM authenticated;

CREATE OR REPLACE FUNCTION public.get_platform_language_stats(
  p_platform TEXT DEFAULT NULL,
  p_language TEXT DEFAULT NULL
)
RETURNS TABLE (
  platform TEXT,
  language TEXT,
  request_count BIGINT,
  last_used TIMESTAMP WITH TIME ZONE
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM internal.admin_users WHERE user_id = auth.uid()
  ) THEN
    RAISE EXCEPTION 'Unauthorized: Admin access required';
  END IF;

  RETURN QUERY
  SELECT s.platform, s.language, s.request_count, s.last_used
  FROM internal.requested_platform_language_stats s
  WHERE (p_platform IS NULL OR s.platform = p_platform)
    AND (p_language IS NULL OR s.language = p_language)
  ORDER BY s.request_count DESC, s.last_used DESC;
END;
$$;

GRANT EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) TO authenticated;
