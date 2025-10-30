CREATE TABLE IF NOT EXISTS internal.admin_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    CONSTRAINT unique_admin_user UNIQUE (user_id)
);

CREATE INDEX IF NOT EXISTS idx_admin_users_user_id ON internal.admin_users(user_id);

ALTER TABLE internal.admin_users ENABLE ROW LEVEL SECURITY;

REVOKE ALL ON internal.admin_users FROM anon;
REVOKE ALL ON internal.admin_users FROM authenticated;

GRANT ALL ON internal.admin_users TO service_role;

CREATE OR REPLACE FUNCTION public.is_admin_user(p_user_id UUID)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
  RETURN EXISTS (
    SELECT 1 FROM internal.admin_users WHERE user_id = p_user_id
  );
END;
$$;

GRANT EXECUTE ON FUNCTION public.is_admin_user(UUID) TO service_role;
