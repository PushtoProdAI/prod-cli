-- Grant execute permissions to authenticated role for get_stack_usage_stats function
GRANT EXECUTE ON FUNCTION public.get_stack_usage_stats(TEXT, TEXT, TEXT, TEXT) TO authenticated;