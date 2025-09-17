-- Password Security Features Migration
-- This migration adds comprehensive password security features to the Prod CLI authentication system

-- Create password history table to prevent password reuse
CREATE TABLE IF NOT EXISTS public.password_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Index for efficient lookups
    CONSTRAINT unique_user_password UNIQUE (user_id, password_hash)
);

-- Create account lockout tracking table
CREATE TABLE IF NOT EXISTS public.account_lockout (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    failed_attempts INTEGER DEFAULT 0,
    locked_until TIMESTAMP WITH TIME ZONE,
    last_failed_attempt TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Only one record per user
    CONSTRAINT unique_user_lockout UNIQUE (user_id)
);

-- Create password policy settings table
CREATE TABLE IF NOT EXISTS public.password_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    min_length INTEGER DEFAULT 8,
    require_uppercase BOOLEAN DEFAULT true,
    require_lowercase BOOLEAN DEFAULT true,
    require_numbers BOOLEAN DEFAULT true,
    require_symbols BOOLEAN DEFAULT true,
    max_age_days INTEGER DEFAULT 90, -- Password expiration
    history_count INTEGER DEFAULT 5, -- Number of previous passwords to remember
    max_failed_attempts INTEGER DEFAULT 5,
    lockout_duration_minutes INTEGER DEFAULT 30,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Insert default password policy
INSERT INTO public.password_policies (id) VALUES (gen_random_uuid())
ON CONFLICT DO NOTHING;

-- Create indexes for performance
CREATE INDEX IF NOT EXISTS idx_password_history_user_id ON public.password_history(user_id);
CREATE INDEX IF NOT EXISTS idx_password_history_created_at ON public.password_history(created_at);
CREATE INDEX IF NOT EXISTS idx_account_lockout_user_id ON public.account_lockout(user_id);
CREATE INDEX IF NOT EXISTS idx_account_lockout_locked_until ON public.account_lockout(locked_until);

-- Function to check if user is locked out
CREATE OR REPLACE FUNCTION public.is_user_locked_out(p_user_id UUID)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    lockout_record RECORD;
BEGIN
    SELECT * INTO lockout_record 
    FROM public.account_lockout 
    WHERE user_id = p_user_id;
    
    IF lockout_record IS NULL THEN
        RETURN FALSE;
    END IF;
    
    -- Check if lockout period has expired
    IF lockout_record.locked_until IS NOT NULL AND lockout_record.locked_until > NOW() THEN
        RETURN TRUE;
    END IF;
    
    RETURN FALSE;
END;
$$;

-- Function to record failed login attempt
CREATE OR REPLACE FUNCTION public.record_failed_login(p_user_id UUID)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    policy_record RECORD;
    lockout_record RECORD;
    new_failed_attempts INTEGER;
    lockout_until TIMESTAMP WITH TIME ZONE;
BEGIN
    -- Get password policy
    SELECT * INTO policy_record FROM public.password_policies LIMIT 1;
    
    -- Get or create lockout record
    SELECT * INTO lockout_record FROM public.account_lockout WHERE user_id = p_user_id;
    
    IF lockout_record IS NULL THEN
        -- Create new lockout record
        INSERT INTO public.account_lockout (user_id, failed_attempts, last_failed_attempt)
        VALUES (p_user_id, 1, NOW());
    ELSE
        -- Update existing record
        new_failed_attempts := lockout_record.failed_attempts + 1;
        
        -- Check if we should lock the account
        IF new_failed_attempts >= policy_record.max_failed_attempts THEN
            lockout_until := NOW() + (policy_record.lockout_duration_minutes || ' minutes')::INTERVAL;
        END IF;
        
        UPDATE public.account_lockout 
        SET 
            failed_attempts = new_failed_attempts,
            last_failed_attempt = NOW(),
            locked_until = lockout_until,
            updated_at = NOW()
        WHERE user_id = p_user_id;
    END IF;
END;
$$;

-- Function to reset failed login attempts on successful login
CREATE OR REPLACE FUNCTION public.reset_failed_logins(p_user_id UUID)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
BEGIN
    UPDATE public.account_lockout 
    SET 
        failed_attempts = 0,
        locked_until = NULL,
        updated_at = NOW()
    WHERE user_id = p_user_id;
END;
$$;

-- Function to check password history
CREATE OR REPLACE FUNCTION public.check_password_history(p_user_id UUID, p_password_hash TEXT)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    policy_record RECORD;
    history_count INTEGER;
BEGIN
    -- Get password policy
    SELECT * INTO policy_record FROM public.password_policies LIMIT 1;
    
    -- Check if password exists in recent history
    SELECT COUNT(*) INTO history_count
    FROM public.password_history
    WHERE user_id = p_user_id 
    AND password_hash = p_password_hash
    AND created_at > NOW() - (policy_record.history_count || ' passwords')::INTERVAL;
    
    RETURN history_count > 0;
END;
$$;

-- Function to add password to history
CREATE OR REPLACE FUNCTION public.add_password_to_history(p_user_id UUID, p_password_hash TEXT)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    policy_record RECORD;
BEGIN
    -- Get password policy
    SELECT * INTO policy_record FROM public.password_policies LIMIT 1;
    
    -- Add new password to history
    INSERT INTO public.password_history (user_id, password_hash)
    VALUES (p_user_id, p_password_hash);
    
    -- Clean up old passwords beyond the history limit
    DELETE FROM public.password_history
    WHERE user_id = p_user_id
    AND id NOT IN (
        SELECT id FROM public.password_history
        WHERE user_id = p_user_id
        ORDER BY created_at DESC
        LIMIT policy_record.history_count
    );
END;
$$;

-- Function to check if password is expired
CREATE OR REPLACE FUNCTION public.is_password_expired(p_user_id UUID)
RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = ''
AS $$
DECLARE
    policy_record RECORD;
    user_record RECORD;
    password_age_days INTEGER;
BEGIN
    -- Get password policy
    SELECT * INTO policy_record FROM public.password_policies LIMIT 1;
    
    -- Get user's password updated time
    SELECT updated_at INTO user_record FROM auth.users WHERE id = p_user_id;
    
    IF user_record IS NULL THEN
        RETURN FALSE;
    END IF;
    
    -- Calculate password age in days
    password_age_days := EXTRACT(EPOCH FROM (NOW() - user_record.updated_at)) / 86400;
    
    RETURN password_age_days > policy_record.max_age_days;
END;
$$;

-- Enable Row Level Security
ALTER TABLE public.password_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.account_lockout ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.password_policies ENABLE ROW LEVEL SECURITY;

-- Revoke access from client roles
REVOKE ALL ON public.password_history FROM anon;
REVOKE ALL ON public.password_history FROM authenticated;
REVOKE ALL ON public.account_lockout FROM anon;
REVOKE ALL ON public.account_lockout FROM authenticated;
REVOKE ALL ON public.password_policies FROM anon;
REVOKE ALL ON public.password_policies FROM authenticated;

-- Grant access only to service role
GRANT ALL ON public.password_history TO service_role;
GRANT ALL ON public.account_lockout TO service_role;
GRANT ALL ON public.password_policies TO service_role;

-- Grant execute permissions on functions to service role
GRANT EXECUTE ON FUNCTION public.is_user_locked_out(UUID) TO service_role;
GRANT EXECUTE ON FUNCTION public.record_failed_login(UUID) TO service_role;
GRANT EXECUTE ON FUNCTION public.reset_failed_logins(UUID) TO service_role;
GRANT EXECUTE ON FUNCTION public.check_password_history(UUID, TEXT) TO service_role;
GRANT EXECUTE ON FUNCTION public.add_password_to_history(UUID, TEXT) TO service_role;
GRANT EXECUTE ON FUNCTION public.is_password_expired(UUID) TO service_role;

-- Create trigger to automatically update updated_at columns
CREATE OR REPLACE FUNCTION public.update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_account_lockout_updated_at
    BEFORE UPDATE ON public.account_lockout
    FOR EACH ROW
    EXECUTE FUNCTION public.update_updated_at_column();

CREATE TRIGGER update_password_policies_updated_at
    BEFORE UPDATE ON public.password_policies
    FOR EACH ROW
    EXECUTE FUNCTION public.update_updated_at_column();
