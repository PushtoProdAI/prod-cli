CREATE TABLE IF NOT EXISTS internal.requested_platform_language_stats (
  platform TEXT NOT NULL,
  language TEXT NOT NULL,
  request_count BIGINT DEFAULT 1,
  last_used TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  PRIMARY KEY (platform, language)
);

CREATE OR REPLACE FUNCTION public.increment_platform_language_usage(
  p_platform TEXT,
  p_language TEXT
)
RETURNS VOID
LANGUAGE SQL
SECURITY DEFINER
SET search_path = ''
AS $$
INSERT INTO internal.requested_platform_language_stats (
  platform, language, request_count, last_used
) VALUES (
  p_platform, p_language, 1, NOW()
)
ON CONFLICT (platform, language)
DO UPDATE SET
  request_count = internal.requested_platform_language_stats.request_count + 1,
  last_used = NOW();
$$;

REVOKE EXECUTE ON FUNCTION public.increment_platform_language_usage(TEXT, TEXT) FROM anon;
REVOKE EXECUTE ON FUNCTION public.increment_platform_language_usage(TEXT, TEXT) FROM authenticated;
REVOKE EXECUTE ON FUNCTION public.increment_platform_language_usage(TEXT, TEXT) FROM public;

GRANT EXECUTE ON FUNCTION public.increment_platform_language_usage(TEXT, TEXT) TO service_role;
GRANT EXECUTE ON FUNCTION public.increment_platform_language_usage(TEXT, TEXT) TO authenticated;

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
  RETURN QUERY
  SELECT s.platform, s.language, s.request_count, s.last_used
  FROM internal.requested_platform_language_stats s
  WHERE (p_platform IS NULL OR s.platform = p_platform)
    AND (p_language IS NULL OR s.language = p_language)
  ORDER BY s.request_count DESC, s.last_used DESC;
END;
$$;

REVOKE EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) FROM anon;
REVOKE EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) FROM authenticated;
REVOKE EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) FROM public;

GRANT EXECUTE ON FUNCTION public.get_platform_language_stats(TEXT, TEXT) TO service_role;
