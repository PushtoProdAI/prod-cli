-- ============================================================================
-- Migration: Update Token Packages for Pricing Strategy
-- ============================================================================

-- Delete old packages (not used in production)
DELETE FROM public.token_packages;

-- Insert new packages
INSERT INTO public.token_packages (name, description, token_count, price_cents, sort_order, active)
VALUES
  (
    'Starter',
    '25 tokens for deploying your projects. Each token covers one standard deployment including code analysis, config generation, and infrastructure provisioning. Perfect for side projects.',
    25,
    1000,  -- $10.00
    1,
    true
  ),
  (
    'Builder',
    '100 tokens for active development. Each token covers one complete deployment cycle including agent orchestration, secrets management, and deployment to your preferred platform. Ideal for developers shipping regularly.',
    100,
    3900,  -- $39.00
    2,
    true
  );

-- Add comment
COMMENT ON TABLE public.token_packages IS 'Token packages for purchase. Pricing strategy: 1 token = 1 standard deployment.';
