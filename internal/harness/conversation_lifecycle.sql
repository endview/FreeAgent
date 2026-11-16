-- Schema 2: lifecycle is stored in meta; writes and routing serialize with
-- retirement on the conversation row. This guard also covers non-UI writers.
CREATE FUNCTION fa.conversation_live(chat text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM fa.meta WHERE key='conversation/lifecycle/'||chat
 AND document->>'state' IN ('trashed','purged'))
$$;
CREATE FUNCTION fa.guard_live_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM id FROM fa.conversations WHERE id=NEW.conversation_id FOR UPDATE;
 IF NOT fa.conversation_live(NEW.conversation_id) THEN
  RAISE EXCEPTION 'conversation unavailable for execution' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER a_lifecycle BEFORE INSERT OR UPDATE ON fa.messages FOR EACH ROW EXECUTE FUNCTION fa.guard_live_record();
CREATE TRIGGER a_lifecycle BEFORE INSERT OR UPDATE ON fa.runs FOR EACH ROW EXECUTE FUNCTION fa.guard_live_record();
CREATE FUNCTION fa.guard_space_route() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.active_conversation_id<>'' AND NOT EXISTS(SELECT 1 FROM fa.conversations c WHERE c.id=NEW.active_conversation_id AND c.space_id=NEW.id AND fa.conversation_live(c.id)) THEN
  RAISE EXCEPTION 'active conversation unavailable' USING ERRCODE='55000';
 END IF;
 IF NEW.default_conversation_id<>'' AND NOT EXISTS(SELECT 1 FROM fa.conversations c WHERE c.id=NEW.default_conversation_id AND c.space_id=NEW.id AND fa.conversation_live(c.id)) THEN
  RAISE EXCEPTION 'default conversation unavailable' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END $$;
-- Spaces are created before their first conversation; only updates validate routes.
CREATE TRIGGER b_lifecycle_route BEFORE UPDATE OF active_conversation_id,default_conversation_id ON fa.spaces FOR EACH ROW EXECUTE FUNCTION fa.guard_space_route();
CREATE FUNCTION fa.guard_live_meta() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d jsonb; chat text;
BEGIN
 IF NEW.key LIKE 'input/%' OR NEW.key LIKE 'channel/operation/%' OR NEW.key LIKE 'channel/render/%' OR NEW.key LIKE 'channel/reply-parts/%' OR NEW.key LIKE 'channel/pending-reply/%' THEN
  d:=NEW.value::jsonb;
  chat:=COALESCE(d->>'conversation_id','');
  IF chat<>'' AND ((NEW.key LIKE 'input/%' AND d->>'status'='pending') OR (NEW.key LIKE 'channel/operation/%' AND d->>'state' IN ('pending','scheduled','held','reviewing','dispatching','paused')) OR NEW.key LIKE 'channel/render/%' OR NEW.key LIKE 'channel/reply-parts/%' OR NEW.key LIKE 'channel/pending-reply/%') THEN
   PERFORM id FROM fa.conversations WHERE id=chat FOR UPDATE;
   IF NOT fa.conversation_live(chat) THEN RAISE EXCEPTION 'conversation unavailable for execution' USING ERRCODE='55000'; END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER b_lifecycle_meta BEFORE INSERT OR UPDATE ON fa.meta FOR EACH ROW EXECUTE FUNCTION fa.guard_live_meta();
