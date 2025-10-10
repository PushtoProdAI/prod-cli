-- Token Operations Functions
-- Secure, atomic operations for token consumption, refunds, and management

-- ============================================================================
-- Function: Consume Tokens (Atomic with Row-Level Locking)
-- ============================================================================
-- This is the CORE security-critical function for token deduction
-- Uses SELECT FOR UPDATE to prevent race conditions
CREATE OR REPLACE FUNCTION public.consume_tokens(
  p_user_id UUID,
  p_tokens_to_consume INTEGER,
  p_operation TEXT,
  p_metadata JSONB DEFAULT '{}'::jsonb
)
RETURNS TABLE(
  success BOOLEAN,
  transaction_id UUID,
  tokens_remaining INTEGER,
  error_message TEXT
) AS $$
DECLARE
  v_plan_tokens INTEGER;
  v_bonus_tokens INTEGER;
  v_used_tokens INTEGER;
  v_available_tokens INTEGER;
  v_tokens_before INTEGER;
  v_tokens_after INTEGER;
  v_transaction_id UUID;
  v_tokens_from_plan INTEGER;
  v_tokens_from_bonus INTEGER;
BEGIN
  -- SECURITY: Ensure user can only consume their own tokens
  IF p_user_id != auth.uid() THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Unauthorized: can only consume your own tokens';
    RETURN;
  END IF;

  -- Input validation
  IF p_tokens_to_consume <= 0 THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Invalid token amount: must be positive';
    RETURN;
  END IF;

  -- Validate operation type
  IF p_operation NOT IN ('deploy', 'dry_run', 'rollback', 'status') THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, format('Invalid operation: %s', p_operation);
    RETURN;
  END IF;

  -- Lock the row for update to prevent race conditions (CRITICAL for security)
  SELECT plan_tokens, bonus_tokens, used_tokens
  INTO v_plan_tokens, v_bonus_tokens, v_used_tokens
  FROM public.token_balances
  WHERE user_id = p_user_id
  FOR UPDATE; -- This prevents concurrent modifications

  -- Check if user exists
  IF NOT FOUND THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'User token balance not found';
    RETURN;
  END IF;

  -- Calculate available tokens
  v_available_tokens := GREATEST(0, v_plan_tokens - v_used_tokens) + v_bonus_tokens;
  v_tokens_before := v_available_tokens;

  -- Check if user has enough tokens
  IF v_available_tokens < p_tokens_to_consume THEN
    RETURN QUERY SELECT
      false,
      NULL::UUID,
      v_available_tokens,
      format('Insufficient tokens: need %s, have %s', p_tokens_to_consume, v_available_tokens);
    RETURN;
  END IF;

  -- Calculate token consumption strategy
  -- Strategy: Consume plan tokens first, then bonus tokens
  v_tokens_from_plan := LEAST(p_tokens_to_consume, GREATEST(0, v_plan_tokens - v_used_tokens));
  v_tokens_from_bonus := p_tokens_to_consume - v_tokens_from_plan;

  -- Update token balance atomically
  UPDATE public.token_balances
  SET
    used_tokens = used_tokens + v_tokens_from_plan,
    bonus_tokens = bonus_tokens - v_tokens_from_bonus,
    updated_at = NOW()
  WHERE user_id = p_user_id;

  -- Calculate remaining tokens after consumption
  v_tokens_after := v_tokens_before - p_tokens_to_consume;

  -- Create immutable transaction record
  INSERT INTO public.token_transactions (
    user_id,
    operation,
    tokens_consumed,
    tokens_before,
    tokens_after,
    metadata
  ) VALUES (
    p_user_id,
    p_operation,
    p_tokens_to_consume,
    v_tokens_before,
    v_tokens_after,
    p_metadata
  )
  RETURNING id INTO v_transaction_id;

  -- Return success
  RETURN QUERY SELECT
    true,
    v_transaction_id,
    v_tokens_after,
    NULL::TEXT;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

-- Grant execute to authenticated users (they can only consume their own tokens due to auth.uid() check)
REVOKE ALL ON FUNCTION public.consume_tokens FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.consume_tokens(UUID, INTEGER, TEXT, JSONB) TO authenticated;

-- ============================================================================
-- Function: Refund Tokens
-- ============================================================================
-- Refunds tokens from a failed or cancelled operation
CREATE OR REPLACE FUNCTION public.refund_tokens(
  p_original_transaction_id UUID,
  p_refund_reason TEXT DEFAULT 'Operation failed'
)
RETURNS TABLE(
  success BOOLEAN,
  transaction_id UUID,
  tokens_refunded INTEGER,
  error_message TEXT
) AS $$
DECLARE
  v_user_id UUID;
  v_tokens_consumed INTEGER;
  v_operation TEXT;
  v_original_metadata JSONB;
  v_transaction_id UUID;
  v_tokens_before INTEGER;
  v_tokens_after INTEGER;
BEGIN
  -- Get original transaction details
  SELECT user_id, tokens_consumed, operation, metadata
  INTO v_user_id, v_tokens_consumed, v_operation, v_original_metadata
  FROM public.token_transactions
  WHERE id = p_original_transaction_id;

  IF NOT FOUND THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Original transaction not found';
    RETURN;
  END IF;

  -- Prevent refunding negative amounts (which would be additions)
  IF v_tokens_consumed <= 0 THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Cannot refund a non-consumption transaction';
    RETURN;
  END IF;

  -- Check if already refunded (prevent double refunds)
  IF EXISTS (
    SELECT 1 FROM public.token_transactions
    WHERE operation = 'refund'
    AND metadata->>'original_transaction_id' = p_original_transaction_id::TEXT
  ) THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Transaction already refunded';
    RETURN;
  END IF;

  -- Get current balance
  SELECT public.get_available_tokens(v_user_id) INTO v_tokens_before;

  -- Refund strategy: Add back to bonus tokens (simpler than reversing consumption logic)
  UPDATE public.token_balances
  SET
    bonus_tokens = bonus_tokens + v_tokens_consumed,
    updated_at = NOW()
  WHERE user_id = v_user_id;

  v_tokens_after := v_tokens_before + v_tokens_consumed;

  -- Create refund transaction record
  INSERT INTO public.token_transactions (
    user_id,
    operation,
    tokens_consumed,
    tokens_before,
    tokens_after,
    metadata
  ) VALUES (
    v_user_id,
    'refund',
    -v_tokens_consumed, -- Negative indicates addition
    v_tokens_before,
    v_tokens_after,
    jsonb_build_object(
      'original_transaction_id', p_original_transaction_id,
      'original_operation', v_operation,
      'refund_reason', p_refund_reason,
      'original_metadata', v_original_metadata
    )
  )
  RETURNING id INTO v_transaction_id;

  RETURN QUERY SELECT
    true,
    v_transaction_id,
    v_tokens_consumed,
    NULL::TEXT;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

REVOKE ALL ON FUNCTION public.refund_tokens FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.refund_tokens TO service_role;

-- ============================================================================
-- Function: Add Purchased Tokens
-- ============================================================================
-- Adds tokens from a completed purchase
CREATE OR REPLACE FUNCTION public.add_purchased_tokens(
  p_user_id UUID,
  p_tokens_to_add INTEGER,
  p_purchase_id UUID DEFAULT NULL
)
RETURNS TABLE(
  success BOOLEAN,
  transaction_id UUID,
  new_balance INTEGER,
  error_message TEXT
) AS $$
DECLARE
  v_transaction_id UUID;
  v_tokens_before INTEGER;
  v_tokens_after INTEGER;
BEGIN
  IF p_tokens_to_add <= 0 THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'Invalid token amount';
    RETURN;
  END IF;

  -- Get current balance
  SELECT public.get_available_tokens(p_user_id) INTO v_tokens_before;

  -- Add to bonus tokens (purchased tokens don't expire)
  UPDATE public.token_balances
  SET
    bonus_tokens = bonus_tokens + p_tokens_to_add,
    updated_at = NOW()
  WHERE user_id = p_user_id;

  IF NOT FOUND THEN
    RETURN QUERY SELECT false, NULL::UUID, 0, 'User not found';
    RETURN;
  END IF;

  v_tokens_after := v_tokens_before + p_tokens_to_add;

  -- Record transaction
  INSERT INTO public.token_transactions (
    user_id,
    operation,
    tokens_consumed,
    tokens_before,
    tokens_after,
    metadata
  ) VALUES (
    p_user_id,
    'purchase',
    -p_tokens_to_add, -- Negative indicates addition
    v_tokens_before,
    v_tokens_after,
    jsonb_build_object('purchase_id', p_purchase_id)
  )
  RETURNING id INTO v_transaction_id;

  RETURN QUERY SELECT
    true,
    v_transaction_id,
    v_tokens_after,
    NULL::TEXT;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

REVOKE ALL ON FUNCTION public.add_purchased_tokens FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.add_purchased_tokens TO service_role;

-- ============================================================================
-- Function: Reset Monthly Tokens
-- ============================================================================
-- Resets monthly plan tokens (called by scheduled job)
CREATE OR REPLACE FUNCTION public.reset_monthly_tokens()
RETURNS TABLE(
  users_reset INTEGER,
  total_tokens_reset INTEGER
) AS $$
DECLARE
  v_users_reset INTEGER := 0;
  v_total_tokens INTEGER := 0;
  v_record RECORD;
BEGIN
  -- Reset all users whose reset_date has passed
  FOR v_record IN
    SELECT user_id, plan_tokens, used_tokens
    FROM public.token_balances
    WHERE reset_date <= NOW()
    FOR UPDATE
  LOOP
    -- Record the reset transaction
    INSERT INTO public.token_transactions (
      user_id,
      operation,
      tokens_consumed,
      tokens_before,
      tokens_after,
      metadata
    ) VALUES (
      v_record.user_id,
      'monthly_reset',
      0, -- No consumption, just reset
      GREATEST(0, v_record.plan_tokens - v_record.used_tokens),
      v_record.plan_tokens, -- Full allocation after reset
      jsonb_build_object(
        'previous_used', v_record.used_tokens,
        'reset_date', NOW()
      )
    );

    -- Reset used tokens and update reset date
    UPDATE public.token_balances
    SET
      used_tokens = 0,
      reset_date = date_trunc('month', reset_date) + INTERVAL '1 month',
      updated_at = NOW()
    WHERE user_id = v_record.user_id;

    v_users_reset := v_users_reset + 1;
    v_total_tokens := v_total_tokens + v_record.plan_tokens;
  END LOOP;

  RETURN QUERY SELECT v_users_reset, v_total_tokens;
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

REVOKE ALL ON FUNCTION public.reset_monthly_tokens FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.reset_monthly_tokens TO service_role;

-- ============================================================================
-- Function: Get User Token Summary
-- ============================================================================
-- Returns detailed token information for a user (CLI display)
CREATE OR REPLACE FUNCTION public.get_token_summary(p_user_id UUID)
RETURNS TABLE(
  plan_tokens INTEGER,
  bonus_tokens INTEGER,
  used_tokens INTEGER,
  available_tokens INTEGER,
  reset_date TIMESTAMP WITH TIME ZONE,
  usage_percentage NUMERIC,
  recent_transactions JSONB
) AS $$
DECLARE
  v_plan_tokens INTEGER;
  v_bonus_tokens INTEGER;
  v_used_tokens INTEGER;
  v_available INTEGER;
  v_reset_date TIMESTAMP WITH TIME ZONE;
  v_usage_pct NUMERIC;
  v_recent JSONB;
BEGIN
  -- SECURITY: Ensure user can only view their own token summary
  IF p_user_id != auth.uid() THEN
    RETURN QUERY SELECT 0, 0, 0, 0, NULL::TIMESTAMP WITH TIME ZONE, 0::NUMERIC, '[]'::JSONB;
    RETURN;
  END IF;

  -- Get balance information
  SELECT
    b.plan_tokens,
    b.bonus_tokens,
    b.used_tokens,
    b.reset_date
  INTO
    v_plan_tokens,
    v_bonus_tokens,
    v_used_tokens,
    v_reset_date
  FROM public.token_balances b
  WHERE b.user_id = p_user_id;

  IF NOT FOUND THEN
    RETURN QUERY SELECT 0, 0, 0, 0, NULL::TIMESTAMP WITH TIME ZONE, 0::NUMERIC, '[]'::JSONB;
    RETURN;
  END IF;

  -- Calculate derived values
  v_available := public.get_available_tokens(p_user_id);
  v_usage_pct := CASE
    WHEN v_plan_tokens > 0 THEN (v_used_tokens::NUMERIC / v_plan_tokens::NUMERIC) * 100
    ELSE 0
  END;

  -- Get recent transactions (last 10)
  SELECT jsonb_agg(
    jsonb_build_object(
      'id', t.id,
      'operation', t.operation,
      'tokens_consumed', t.tokens_consumed,
      'created_at', t.created_at,
      'metadata', t.metadata
    ) ORDER BY t.created_at DESC
  )
  INTO v_recent
  FROM (
    SELECT * FROM public.token_transactions
    WHERE user_id = p_user_id
    ORDER BY created_at DESC
    LIMIT 10
  ) t;

  RETURN QUERY SELECT
    v_plan_tokens,
    v_bonus_tokens,
    v_used_tokens,
    v_available,
    v_reset_date,
    v_usage_pct,
    COALESCE(v_recent, '[]'::JSONB);
END;
$$ LANGUAGE plpgsql SECURITY DEFINER;

REVOKE ALL ON FUNCTION public.get_token_summary FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.get_token_summary(UUID) TO authenticated;

-- ============================================================================
-- Comments
-- ============================================================================
COMMENT ON FUNCTION public.consume_tokens IS 'Atomically consume tokens with row-level locking. Authenticated users can only consume their own tokens (validated via auth.uid())';
COMMENT ON FUNCTION public.refund_tokens IS 'Refund tokens from failed operations (service role only)';
COMMENT ON FUNCTION public.add_purchased_tokens IS 'Add tokens from completed purchases (service role only)';
COMMENT ON FUNCTION public.reset_monthly_tokens IS 'Reset monthly token allocations (scheduled job)';
COMMENT ON FUNCTION public.get_token_summary IS 'Get detailed token information for CLI display. Authenticated users can only view their own summary (validated via auth.uid())';
