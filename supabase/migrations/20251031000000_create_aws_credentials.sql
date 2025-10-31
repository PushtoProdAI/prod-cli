-- AWS Credentials Table
-- Stores AWS role ARN and external ID for cross-account access

-- ============================================================================
-- AWS Credentials Table
-- ============================================================================
-- Stores the IAM role information for deploying to customer AWS accounts
CREATE TABLE IF NOT EXISTS public.aws_credentials (
  user_id UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,

  -- AWS Role Information
  external_id TEXT NOT NULL,
  role_arn TEXT,  -- Nullable during setup, required after completion
  region TEXT NOT NULL DEFAULT 'us-east-1',  -- User's preferred deployment region

  -- Metadata
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

  -- Constraints for data integrity
  CONSTRAINT external_id_not_empty CHECK (LENGTH(external_id) > 0),
  CONSTRAINT role_arn_format CHECK (role_arn IS NULL OR role_arn ~ '^arn:aws:iam::[0-9]{12}:role/[a-zA-Z0-9+=,.@_-]+$'),
  CONSTRAINT valid_region CHECK (LENGTH(region) > 0)
);

-- Create indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_aws_credentials_user_id ON public.aws_credentials(user_id);
CREATE INDEX IF NOT EXISTS idx_aws_credentials_created_at ON public.aws_credentials(created_at);

-- ============================================================================
-- Row Level Security (RLS) Policies
-- ============================================================================

-- Enable RLS
ALTER TABLE public.aws_credentials ENABLE ROW LEVEL SECURITY;

-- Users can view their own AWS credentials
CREATE POLICY "Users can view their own AWS credentials"
  ON public.aws_credentials FOR SELECT
  USING (auth.uid() = user_id);

-- Users can insert their own AWS credentials
CREATE POLICY "Users can insert their own AWS credentials"
  ON public.aws_credentials FOR INSERT
  WITH CHECK (auth.uid() = user_id);

-- Users can update their own AWS credentials
CREATE POLICY "Users can update their own AWS credentials"
  ON public.aws_credentials FOR UPDATE
  USING (auth.uid() = user_id);

-- Users can delete their own AWS credentials
CREATE POLICY "Users can delete their own AWS credentials"
  ON public.aws_credentials FOR DELETE
  USING (auth.uid() = user_id);

-- Service role has full access
CREATE POLICY "Service role has full access to AWS credentials"
  ON public.aws_credentials FOR ALL
  USING (auth.role() = 'service_role');

-- ============================================================================
-- Database Functions
-- ============================================================================

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION public.handle_aws_credentials_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Trigger for updated_at
CREATE TRIGGER set_updated_at_aws_credentials
  BEFORE UPDATE ON public.aws_credentials
  FOR EACH ROW EXECUTE FUNCTION public.handle_aws_credentials_updated_at();

-- ============================================================================
-- Check AWS Authentication Status Function
-- ============================================================================
-- Returns true if the authenticated user has completed AWS credentials setup
-- (i.e., both external_id and role_arn are present)
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

-- Grant execute permission to authenticated users
REVOKE ALL ON FUNCTION public.check_aws_authentication FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.check_aws_authentication() TO authenticated;

-- ============================================================================
-- Comments for documentation
-- ============================================================================
COMMENT ON TABLE public.aws_credentials IS 'Stores AWS IAM role information for cross-account deployments';
COMMENT ON COLUMN public.aws_credentials.external_id IS 'Unique external ID for AWS STS assume role security';
COMMENT ON COLUMN public.aws_credentials.role_arn IS 'ARN of the IAM role in the customer AWS account';
COMMENT ON COLUMN public.aws_credentials.region IS 'Default AWS region for deployments';
COMMENT ON FUNCTION public.check_aws_authentication IS 'Check if authenticated user has AWS credentials configured';
