-- Create wrapper functions in public schema for deployment logging
-- This allows Edge Functions to call the audit functions

-- Wrapper for log_deployment_operation
CREATE OR REPLACE FUNCTION public.log_deployment_operation(
    p_user_id UUID,
    p_operation_type TEXT,
    p_resource_type TEXT,
    p_resource_id TEXT,
    p_resource_name TEXT DEFAULT NULL,
    p_status TEXT DEFAULT 'started',
    p_platform TEXT DEFAULT NULL,
    p_language TEXT DEFAULT NULL,
    p_service_type TEXT DEFAULT NULL,
    p_service_provider TEXT DEFAULT NULL,
    p_deployment_config JSONB DEFAULT NULL,
    p_error_message TEXT DEFAULT NULL,
    p_ip_address INET DEFAULT NULL,
    p_user_agent TEXT DEFAULT NULL,
    p_metadata JSONB DEFAULT NULL
)
RETURNS UUID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
    RETURN audit.log_deployment_operation(
        p_user_id,
        p_operation_type,
        p_resource_type,
        p_resource_id,
        p_resource_name,
        p_status,
        p_platform,
        p_language,
        p_service_type,
        p_service_provider,
        p_deployment_config,
        p_error_message,
        p_ip_address,
        p_user_agent,
        p_metadata
    );
END;
$$;

-- Wrapper for update_deployment_operation
CREATE OR REPLACE FUNCTION public.update_deployment_operation(
    p_operation_id UUID,
    p_status TEXT,
    p_error_message TEXT DEFAULT NULL,
    p_metadata JSONB DEFAULT NULL
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
    PERFORM audit.update_deployment_operation(
        p_operation_id,
        p_status,
        p_error_message,
        p_metadata
    );
END;
$$;

-- Grant execute permissions to service role
GRANT EXECUTE ON FUNCTION public.log_deployment_operation(UUID, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, TEXT, JSONB, TEXT, INET, TEXT, JSONB) TO service_role;
GRANT EXECUTE ON FUNCTION public.update_deployment_operation(UUID, TEXT, TEXT, JSONB) TO service_role;
