-- Function to get total used tokens across all users (admin only)
CREATE OR REPLACE FUNCTION public.get_total_used_tokens()
RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN (SELECT COALESCE(SUM(used_tokens), 0) FROM public.token_balances);
END;
$$;

GRANT EXECUTE ON FUNCTION public.get_total_used_tokens() TO service_role;

-- Function to get LLM usage stats grouped by model with aggregations
CREATE OR REPLACE FUNCTION public.get_llm_usage_by_model(
  p_period TEXT DEFAULT 'all'
)
RETURNS TABLE (
  model TEXT,
  total_requests BIGINT,
  input_tokens BIGINT,
  output_tokens BIGINT,
  total_tokens BIGINT,
  total_cost NUMERIC
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
  v_start_date TIMESTAMP WITH TIME ZONE;
  v_end_date TIMESTAMP WITH TIME ZONE;
BEGIN
  -- Parse period parameter
  IF p_period = 'all' THEN
    v_start_date := '-infinity'::TIMESTAMP WITH TIME ZONE;
    v_end_date := 'infinity'::TIMESTAMP WITH TIME ZONE;
  ELSE
    -- Validate YYYY-MM format
    IF p_period !~ '^\d{4}-\d{2}$' THEN
      RAISE EXCEPTION 'Invalid period format. Use YYYY-MM or "all"';
    END IF;
    
    v_start_date := (p_period || '-01')::TIMESTAMP WITH TIME ZONE;
    v_end_date := (v_start_date + INTERVAL '1 month' - INTERVAL '1 second');
  END IF;

  RETURN QUERY
  SELECT 
    l.model_used,
    COUNT(*)::BIGINT,
    COALESCE(SUM(l.prompt_tokens), 0)::BIGINT,
    COALESCE(SUM(l.completion_tokens), 0)::BIGINT,
    COALESCE(SUM(l.tokens_used), 0)::BIGINT,
    COALESCE(SUM(l.cost), 0)::NUMERIC
  FROM public.llm_usage_logs l
  WHERE l.created_at >= v_start_date 
    AND l.created_at <= v_end_date
  GROUP BY l.model_used
  ORDER BY COUNT(*) DESC;
END;
$$;

GRANT EXECUTE ON FUNCTION public.get_llm_usage_by_model(TEXT) TO service_role;
