-- Token Accounting System Migration
-- This creates the foundational tables for token-based usage tracking

-- ============================================================================
-- Token Balances Table
-- ============================================================================
-- Stores current token balance for each user with plan and bonus allocations
CREATE TABLE IF NOT EXISTS public.token_balances (
  user_id UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,

  -- Token allocations
  plan_tokens INTEGER NOT NULL DEFAULT 100, -- Monthly allocation from subscription plan
  bonus_tokens INTEGER NOT NULL DEFAULT 0,  -- One-time bonus tokens (never expire)
  used_tokens INTEGER NOT NULL DEFAULT 0,   -- Tokens consumed this period

  -- Metadata
  reset_date TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT (date_trunc('month', NOW()) + INTERVAL '1 month'),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

  -- Constraints for data integrity
  CONSTRAINT plan_tokens_non_negative CHECK (plan_tokens >= 0),
  CONSTRAINT bonus_tokens_non_negative CHECK (bonus_tokens >= 0),
  CONSTRAINT used_tokens_non_negative CHECK (used_tokens >= 0)
);

-- Create indexes for efficient queries
CREATE INDEX IF NOT EXISTS idx_token_balances_reset_date ON public.token_balances(reset_date);
CREATE INDEX IF NOT EXISTS idx_token_balances_updated_at ON public.token_balances(updated_at);

-- ============================================================================
-- Token Transactions Table (Event Sourcing)
-- ============================================================================
-- Immutable log of all token operations for audit trail and debugging
CREATE TABLE IF NOT EXISTS public.token_transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,

  -- Transaction details
  operation TEXT NOT NULL, -- 'deploy', 'dry_run', 'refund', 'purchase', 'bonus', 'reset'
  tokens_consumed INTEGER NOT NULL, -- Positive for consumption, negative for refunds/additions
  tokens_before INTEGER NOT NULL, -- Balance snapshot before transaction
  tokens_after INTEGER NOT NULL, -- Balance snapshot after transaction

  -- Context and metadata
  metadata JSONB NOT NULL DEFAULT '{}', -- Flexible storage for operation-specific data
  -- Example metadata:
  -- {
  --   "platform": "render",
  --   "project_name": "my-app",
  --   "language": "node",
  --   "llm_model": "gpt-4",
  --   "services_count": 3,
  --   "deployment_id": "uuid",
  --   "refund_reason": "deployment_failed"
  -- }

  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

  -- Constraints
  CONSTRAINT valid_operation CHECK (operation IN ('deploy', 'dry_run', 'refund', 'purchase', 'bonus', 'reset', 'monthly_reset'))
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_token_transactions_user_id ON public.token_transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_token_transactions_created_at ON public.token_transactions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_transactions_operation ON public.token_transactions(operation);
CREATE INDEX IF NOT EXISTS idx_token_transactions_user_date ON public.token_transactions(user_id, created_at DESC);

-- GIN index for JSONB metadata queries
CREATE INDEX IF NOT EXISTS idx_token_transactions_metadata ON public.token_transactions USING GIN (metadata);

-- Partial index for fast refund duplicate checking
CREATE INDEX IF NOT EXISTS idx_token_transactions_refund_lookup
  ON public.token_transactions(operation, (metadata->>'original_transaction_id'))
  WHERE operation = 'refund';

-- ============================================================================
-- Token Packages Table
-- ============================================================================
-- Pre-defined token packages for purchase (pay-as-you-go)
CREATE TABLE IF NOT EXISTS public.token_packages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),

  -- Package details
  name TEXT NOT NULL UNIQUE, -- e.g., "Starter Pack", "Pro Bundle", "Enterprise"
  description TEXT,
  token_count INTEGER NOT NULL, -- Number of tokens in package
  price_cents INTEGER NOT NULL, -- Price in cents (USD)

  -- Metadata
  active BOOLEAN NOT NULL DEFAULT true, -- Can be disabled without deleting
  sort_order INTEGER NOT NULL DEFAULT 0, -- Display order

  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

  -- Constraints
  CONSTRAINT token_count_positive CHECK (token_count > 0),
  CONSTRAINT price_positive CHECK (price_cents > 0)
);

-- Index for active packages lookup
CREATE INDEX IF NOT EXISTS idx_token_packages_active ON public.token_packages(active, sort_order);

-- ============================================================================
-- Token Purchases Table
-- ============================================================================
-- Records of token purchases for financial tracking
CREATE TABLE IF NOT EXISTS public.token_purchases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  package_id UUID NOT NULL REFERENCES public.token_packages(id),

  -- Purchase details
  tokens_purchased INTEGER NOT NULL,
  price_paid_cents INTEGER NOT NULL,

  -- Payment provider details (for future Stripe integration)
  payment_provider TEXT NOT NULL DEFAULT 'stripe', -- 'stripe', 'paypal', etc.
  payment_id TEXT, -- External payment ID from provider
  payment_status TEXT NOT NULL DEFAULT 'pending', -- 'pending', 'completed', 'failed', 'refunded'

  -- Metadata
  metadata JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

  -- Constraints
  CONSTRAINT tokens_purchased_positive CHECK (tokens_purchased > 0),
  CONSTRAINT price_paid_positive CHECK (price_paid_cents > 0),
  CONSTRAINT valid_payment_status CHECK (payment_status IN ('pending', 'completed', 'failed', 'refunded'))
);

-- Indexes for purchase history queries
CREATE INDEX IF NOT EXISTS idx_token_purchases_user_id ON public.token_purchases(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_token_purchases_payment_id ON public.token_purchases(payment_id);
CREATE INDEX IF NOT EXISTS idx_token_purchases_status ON public.token_purchases(payment_status);

-- ============================================================================
-- Row Level Security (RLS) Policies
-- ============================================================================

-- Enable RLS on all tables
ALTER TABLE public.token_balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.token_transactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.token_packages ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.token_purchases ENABLE ROW LEVEL SECURITY;

-- Token Balances Policies
CREATE POLICY "Users can view their own token balance"
  ON public.token_balances FOR SELECT
  USING (auth.uid() = user_id);

CREATE POLICY "Users cannot modify their own balance directly"
  ON public.token_balances FOR UPDATE
  USING (false); -- Only service role can update

CREATE POLICY "Service role has full access to token balances"
  ON public.token_balances FOR ALL
  USING (auth.role() = 'service_role');

-- Token Transactions Policies (read-only for users)
CREATE POLICY "Users can view their own transactions"
  ON public.token_transactions FOR SELECT
  USING (auth.uid() = user_id);

CREATE POLICY "Service role can insert transactions"
  ON public.token_transactions FOR INSERT
  WITH CHECK (auth.role() = 'service_role');

CREATE POLICY "Service role can view all transactions"
  ON public.token_transactions FOR SELECT
  USING (auth.role() = 'service_role');

-- Token Packages Policies (public read)
CREATE POLICY "Anyone can view active packages"
  ON public.token_packages FOR SELECT
  USING (active = true);

CREATE POLICY "Service role can manage packages"
  ON public.token_packages FOR ALL
  USING (auth.role() = 'service_role');

-- Token Purchases Policies
CREATE POLICY "Users can view their own purchases"
  ON public.token_purchases FOR SELECT
  USING (auth.uid() = user_id);

CREATE POLICY "Service role can manage purchases"
  ON public.token_purchases FOR ALL
  USING (auth.role() = 'service_role');

-- ============================================================================
-- Database Functions
-- ============================================================================

-- Function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION public.handle_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Triggers for updated_at
CREATE TRIGGER set_updated_at_token_balances
  BEFORE UPDATE ON public.token_balances
  FOR EACH ROW EXECUTE FUNCTION public.handle_updated_at();

CREATE TRIGGER set_updated_at_token_packages
  BEFORE UPDATE ON public.token_packages
  FOR EACH ROW EXECUTE FUNCTION public.handle_updated_at();

CREATE TRIGGER set_updated_at_token_purchases
  BEFORE UPDATE ON public.token_purchases
  FOR EACH ROW EXECUTE FUNCTION public.handle_updated_at();

-- ============================================================================
-- Initialize User Token Balance Function
-- ============================================================================
-- Automatically create token balance when new user signs up
CREATE OR REPLACE FUNCTION public.initialize_user_token_balance()
RETURNS TRIGGER AS $$
BEGIN
  INSERT INTO public.token_balances (user_id, plan_tokens, bonus_tokens, used_tokens)
  VALUES (NEW.id, 100, 0, 0)
  ON CONFLICT (user_id) DO NOTHING;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Trigger to initialize balance on user creation
CREATE TRIGGER on_auth_user_created_initialize_balance
  AFTER INSERT ON auth.users
  FOR EACH ROW EXECUTE FUNCTION public.initialize_user_token_balance();

-- ============================================================================
-- Helper Function: Get Available Token Balance
-- ============================================================================
-- Returns total available tokens (plan + bonus - used)
CREATE OR REPLACE FUNCTION public.get_available_tokens(p_user_id UUID)
RETURNS INTEGER AS $$
DECLARE
  v_plan_tokens INTEGER;
  v_bonus_tokens INTEGER;
  v_used_tokens INTEGER;
  v_available INTEGER;
BEGIN
  SELECT plan_tokens, bonus_tokens, used_tokens
  INTO v_plan_tokens, v_bonus_tokens, v_used_tokens
  FROM public.token_balances
  WHERE user_id = p_user_id;

  IF NOT FOUND THEN
    RETURN 0;
  END IF;

  -- Available = (plan_tokens - used_tokens) + bonus_tokens
  -- But used_tokens can only go up to plan_tokens, then bonus_tokens are consumed
  v_available := GREATEST(0, v_plan_tokens - v_used_tokens) + v_bonus_tokens;

  RETURN v_available;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Grant execute permission to authenticated users
REVOKE ALL ON FUNCTION public.get_available_tokens FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.get_available_tokens(UUID) TO authenticated;

-- ============================================================================
-- Seed Data: Default Token Packages
-- ============================================================================
INSERT INTO public.token_packages (name, description, token_count, price_cents, sort_order, active)
VALUES
  ('Starter Pack', '50 additional tokens for small projects', 50, 500, 1, true),
  ('Developer Pack', '200 tokens for active development', 200, 1500, 2, true),
  ('Pro Pack', '500 tokens for professional use', 500, 3000, 3, true),
  ('Enterprise Pack', '1000 tokens for large deployments', 1000, 5000, 4, true)
ON CONFLICT (name) DO NOTHING;

-- NOTE: Token pricing rules are currently defined in Go code (cli/internal/tokens/rules.go)
-- This could be moved to the database in the future for dynamic pricing via admin UI

-- ============================================================================
-- Comments for documentation
-- ============================================================================
COMMENT ON TABLE public.token_balances IS 'Stores current token balance for each user with monthly plan allocation';
COMMENT ON TABLE public.token_transactions IS 'Immutable event log of all token operations for audit trail';
COMMENT ON TABLE public.token_packages IS 'Pre-defined token packages available for purchase';
COMMENT ON TABLE public.token_purchases IS 'Financial records of token purchases';
COMMENT ON FUNCTION public.get_available_tokens IS 'Calculate total available tokens for a user (plan + bonus - used)';
