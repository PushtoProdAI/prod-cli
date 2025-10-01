-- Deployment Operations Logging Migration
-- This migration adds comprehensive deployment operation logging

-- Create deployment operations audit table
CREATE TABLE IF NOT EXISTS audit.deployment_operations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES auth.users(id) ON DELETE SET NULL,
    operation_type TEXT NOT NULL, -- 'deploy', 'rollback', 'scale', 'delete', etc.
    resource_type TEXT NOT NULL, -- 'stack', 'service', 'container', etc.
    resource_id TEXT NOT NULL,
    resource_name TEXT,
    status TEXT NOT NULL, -- 'started', 'success', 'failed', 'cancelled'
    platform TEXT,
    language TEXT,
    service_type TEXT,
    service_provider TEXT,
    deployment_config JSONB,
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    completed_at TIMESTAMP WITH TIME ZONE,
    duration_seconds INTEGER,
    ip_address INET,
    user_agent TEXT,
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_deployment_operations_user_id ON audit.deployment_operations(user_id);
CREATE INDEX IF NOT EXISTS idx_deployment_operations_status ON audit.deployment_operations(status);
CREATE INDEX IF NOT EXISTS idx_deployment_operations_created_at ON audit.deployment_operations(created_at);
CREATE INDEX IF NOT EXISTS idx_deployment_operations_resource ON audit.deployment_operations(resource_type, resource_id);

-- Create function to log deployment operations
CREATE OR REPLACE FUNCTION audit.log_deployment_operation(
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
DECLARE
    operation_id UUID;
    start_time TIMESTAMP WITH TIME ZONE;
BEGIN
    operation_id := gen_random_uuid();
    start_time := NOW();
    
    INSERT INTO audit.deployment_operations (
        id, user_id, operation_type, resource_type, resource_id, resource_name,
        status, platform, language, service_type, service_provider,
        deployment_config, error_message, started_at, ip_address, user_agent, metadata
    ) VALUES (
        operation_id, p_user_id, p_operation_type, p_resource_type, p_resource_id, p_resource_name,
        p_status, p_platform, p_language, p_service_type, p_service_provider,
        p_deployment_config, p_error_message, start_time, p_ip_address, p_user_agent, p_metadata
    );
    
    RETURN operation_id;
END;
$$;

-- Create function to update deployment operation status
CREATE OR REPLACE FUNCTION audit.update_deployment_operation(
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
DECLARE
    start_time TIMESTAMP WITH TIME ZONE;
    end_time TIMESTAMP WITH TIME ZONE;
    duration_sec INTEGER;
BEGIN
    end_time := NOW();
    
    -- Get start time and calculate duration
    SELECT started_at INTO start_time 
    FROM audit.deployment_operations 
    WHERE id = p_operation_id;
    
    IF start_time IS NOT NULL THEN
        duration_sec := EXTRACT(EPOCH FROM (end_time - start_time))::INTEGER;
    END IF;
    
    UPDATE audit.deployment_operations 
    SET 
        status = p_status,
        error_message = p_error_message,
        completed_at = end_time,
        duration_seconds = duration_sec,
        metadata = COALESCE(p_metadata, metadata)
    WHERE id = p_operation_id;
END;
$$;

-- Enable Row Level Security
ALTER TABLE audit.deployment_operations ENABLE ROW LEVEL SECURITY;

-- Revoke access from client roles
REVOKE ALL ON audit.deployment_operations FROM anon;
REVOKE ALL ON audit.deployment_operations FROM authenticated;

-- Grant access only to service role
GRANT ALL ON audit.deployment_operations TO service_role;

-- Grant execute permissions on functions to service role
GRANT EXECUTE ON FUNCTION audit.log_deployment_operation TO service_role;
GRANT EXECUTE ON FUNCTION audit.update_deployment_operation TO service_role;
