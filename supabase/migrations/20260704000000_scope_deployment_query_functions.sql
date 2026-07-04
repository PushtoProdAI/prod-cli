-- Security fix: enforce multi-tenant scoping inside the deployment query functions.
--
-- Both query_deployment_operations and count_deployment_operations are
-- SECURITY DEFINER and granted to the `authenticated` role, so any authenticated
-- user can invoke them directly via PostgREST (POST /rest/v1/rpc/...), bypassing
-- the app-layer scoping done in the deployment-logger Edge Function. Previously
-- the caller-supplied p_user_id (including NULL = "all users") was trusted with
-- no verification, allowing IDOR / cross-tenant reads of audit.deployment_operations.
--
-- Fix: derive the caller from auth.uid() and only honor a NULL or foreign
-- p_user_id when the caller is an admin (public.is_admin_user). Non-admin callers
-- are always constrained to their own rows. The Edge Function keeps working
-- unchanged: admins pass p_user_id => NULL and pass the gate; regular users pass
-- their own id.

CREATE OR REPLACE FUNCTION public.query_deployment_operations(
  p_user_id UUID DEFAULT NULL,          -- NULL = all users; only honored for admins
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
DECLARE
  v_caller UUID := auth.uid();
  v_is_admin BOOLEAN;
BEGIN
  IF v_caller IS NULL THEN
    RAISE EXCEPTION 'not authenticated';
  END IF;

  SELECT public.is_admin_user(v_caller) INTO v_is_admin;

  -- Only admins may request all users (NULL) or another user's rows.
  IF (p_user_id IS NULL OR p_user_id <> v_caller) AND NOT v_is_admin THEN
    RAISE EXCEPTION 'forbidden';
  END IF;

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
DECLARE
  v_caller UUID := auth.uid();
  v_is_admin BOOLEAN;
BEGIN
  IF v_caller IS NULL THEN
    RAISE EXCEPTION 'not authenticated';
  END IF;

  SELECT public.is_admin_user(v_caller) INTO v_is_admin;

  IF (p_user_id IS NULL OR p_user_id <> v_caller) AND NOT v_is_admin THEN
    RAISE EXCEPTION 'forbidden';
  END IF;

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
