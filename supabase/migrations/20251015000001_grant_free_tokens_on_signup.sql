-- ============================================================================
-- Migration: Grant Free Tokens on User Signup
-- ============================================================================
-- Description: Automatically grants 5 free tokens when a new user signs up

-- Create function to grant welcome tokens
CREATE OR REPLACE FUNCTION public.grant_welcome_tokens()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  -- Create token balance entry for new user
  INSERT INTO public.token_balances (user_id, plan_tokens, bonus_tokens, used_tokens)
  VALUES (NEW.id, 0, 5, 0)
  ON CONFLICT (user_id) DO NOTHING;

  -- Record the bonus grant in transactions table
  INSERT INTO public.token_transactions (
    user_id,
    amount,
    operation,
    metadata
  )
  VALUES (
    NEW.id,
    5,
    'bonus_grant',
    jsonb_build_object(
      'reason', 'welcome_bonus',
      'description', 'Welcome to Prod CLI! Here are 5 free tokens to get you started.'
    )
  );

  RETURN NEW;
END;
$$;

-- Create trigger to run on user creation
DROP TRIGGER IF EXISTS on_auth_user_created_grant_tokens ON auth.users;

CREATE TRIGGER on_auth_user_created_grant_tokens
  AFTER INSERT ON auth.users
  FOR EACH ROW
  EXECUTE FUNCTION public.grant_welcome_tokens();

-- Add comment
COMMENT ON FUNCTION public.grant_welcome_tokens IS 'Automatically grants 5 welcome tokens to new users on signup';
