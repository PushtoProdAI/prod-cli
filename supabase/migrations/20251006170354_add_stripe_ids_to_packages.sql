-- ============================================================================
-- Add Stripe Integration Fields to Token Packages
-- ============================================================================
-- This migration adds Stripe product and price IDs to the token_packages table
-- to enable integration with Stripe Checkout and webhook processing.

-- Add Stripe fields to token_packages
ALTER TABLE public.token_packages
  ADD COLUMN IF NOT EXISTS stripe_product_id TEXT UNIQUE,
  ADD COLUMN IF NOT EXISTS stripe_price_id TEXT UNIQUE;

-- Add indexes for Stripe ID lookups (used in webhook processing)
CREATE INDEX IF NOT EXISTS idx_token_packages_stripe_product_id
  ON public.token_packages(stripe_product_id)
  WHERE stripe_product_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_token_packages_stripe_price_id
  ON public.token_packages(stripe_price_id)
  WHERE stripe_price_id IS NOT NULL;

-- Add comment explaining the fields
COMMENT ON COLUMN public.token_packages.stripe_product_id IS 'Stripe Product ID (e.g., prod_...) - links to Stripe product';
COMMENT ON COLUMN public.token_packages.stripe_price_id IS 'Stripe Price ID (e.g., price_...) - used in checkout sessions';
