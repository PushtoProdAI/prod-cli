CREATE SCHEMA IF NOT EXISTS internal;

CREATE TABLE IF NOT EXISTS internal.requested_stack_usage_stats (
  platform TEXT NOT NULL,
  language TEXT NOT NULL,
  service_type TEXT NOT NULL,
  service_provider TEXT NOT NULL,
  usage_count BIGINT DEFAULT 1,
  last_used TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  PRIMARY KEY (platform, language, service_type, service_provider)
);

CREATE OR REPLACE FUNCTION public.increment_requested_stack_usage(
  p_platform TEXT,
  p_language TEXT,
  p_service_type TEXT,
  p_service_provider TEXT
)
RETURNS VOID
LANGUAGE SQL
SECURITY DEFINER
SET search_path = ''
AS $$
INSERT INTO internal.requested_stack_usage_stats (
  platform, language, service_type, service_provider, usage_count, last_used
) VALUES (
  p_platform, p_language, p_service_type, p_service_provider, 1, NOW()
)
ON CONFLICT (platform, language, service_type, service_provider)
DO UPDATE SET
  usage_count = internal.requested_stack_usage_stats.usage_count + 1,
  last_used = NOW();
$$;

REVOKE EXECUTE ON FUNCTION public.increment_requested_stack_usage(TEXT, TEXT, TEXT, TEXT) FROM anon;
REVOKE EXECUTE ON FUNCTION public.increment_requested_stack_usage(TEXT, TEXT, TEXT, TEXT) FROM authenticated;
REVOKE EXECUTE ON FUNCTION public.increment_requested_stack_usage(TEXT, TEXT, TEXT, TEXT) FROM public;

GRANT EXECUTE ON FUNCTION public.increment_requested_stack_usage(TEXT, TEXT, TEXT, TEXT) TO service_role;
