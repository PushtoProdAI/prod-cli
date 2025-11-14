-- Allow role_arn to be NULL during AWS authentication setup
-- This enables a two-step flow: initialize (get external_id) then complete (add role_arn)

-- Drop the existing NOT NULL constraint on role_arn
ALTER TABLE public.aws_credentials 
  ALTER COLUMN role_arn DROP NOT NULL;

-- Update the check constraint to allow NULL role_arn
ALTER TABLE public.aws_credentials 
  DROP CONSTRAINT IF EXISTS role_arn_format;

ALTER TABLE public.aws_credentials 
  ADD CONSTRAINT role_arn_format 
  CHECK (role_arn IS NULL OR role_arn ~ '^arn:aws:iam::[0-9]{12}:role/[a-zA-Z0-9+=,.@_-]+$');

-- Update the check_aws_authentication function to only return true when setup is complete
CREATE OR REPLACE FUNCTION public.check_aws_authentication()
RETURNS BOOLEAN AS $$
DECLARE
  v_user_id UUID;
  v_exists BOOLEAN;
BEGIN
  -- Get the authenticated user's ID
  v_user_id := auth.uid();

  -- Check if user is authenticated
  IF v_user_id IS NULL THEN
    RETURN false;
  END IF;

  -- Check if complete credentials exist for this user (role_arn must not be NULL)
  SELECT EXISTS(
    SELECT 1
    FROM public.aws_credentials
    WHERE user_id = v_user_id
      AND role_arn IS NOT NULL
  ) INTO v_exists;

  RETURN v_exists;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Update comment
COMMENT ON COLUMN public.aws_credentials.role_arn IS 'IAM role ARN - nullable during setup, required after CloudFormation stack creation';
