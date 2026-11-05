CREATE SCHEMA fa;
CREATE EXTENSION pg_trgm WITH SCHEMA public;

-- Exact input bytes remain in value/request_json/config_json. Documents are
-- typed projections for queries; JSONB serialization never replaces a digest.
CREATE FUNCTION fa.meta_document(k text, v text) RETURNS jsonb
LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT CASE WHEN k IN ('format','schema/version','schema/identity','core/id','attachment-cleanup-cursor')
 OR k LIKE 'schema/%' OR k LIKE 'chat/model/%' OR k LIKE 'chat/agent/%' OR k LIKE 'chat/pin/%'
 OR k LIKE 'collaboration-run/%' OR k LIKE 'attempt/time/%'
 OR k LIKE 'run/limit-next/%' OR k LIKE 'context/maintenance/%'
 OR k LIKE 'command/direct/%' OR k LIKE 'channel/pair-receipt/%'
 THEN to_jsonb(v) ELSE v::jsonb END
$$;
CREATE FUNCTION fa.response_document(v text) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE STRICT AS $$
BEGIN RETURN v::jsonb; EXCEPTION WHEN invalid_text_representation THEN RETURN NULL; END
$$;
CREATE TABLE fa.meta(
 key text COLLATE "C" PRIMARY KEY, value text NOT NULL,
 document jsonb GENERATED ALWAYS AS (fa.meta_document(key,value)) STORED
);
CREATE TABLE fa.spaces(
 id text PRIMARY KEY, title text NOT NULL,
 source text NOT NULL CHECK(source IN ('local','telegram','lark','qq')),
 chat_kind text NOT NULL, external_chat text NOT NULL,
 default_conversation_id text NOT NULL, active_conversation_id text NOT NULL,
 multi_conversation_enabled boolean NOT NULL DEFAULT false,
 created_at text NOT NULL, updated_at text NOT NULL
);
CREATE UNIQUE INDEX external_space_identity ON fa.spaces(source,external_chat) WHERE source!='local' AND external_chat!='';
CREATE TABLE fa.conversations(
 id text PRIMARY KEY,title text NOT NULL,workspace text NOT NULL,
 space_id text NOT NULL REFERENCES fa.spaces(id),created_at text NOT NULL,updated_at text NOT NULL,
 last_message_seq bigint NOT NULL DEFAULT 0 CHECK(last_message_seq>=0),
 last_run_seq bigint NOT NULL DEFAULT 0 CHECK(last_run_seq>=0)
);
CREATE TABLE fa.runs(
 id text PRIMARY KEY, conversation_id text NOT NULL REFERENCES fa.conversations(id),
 request_key text NOT NULL,prompt text NOT NULL,config_json text NOT NULL,
 config_doc jsonb GENERATED ALWAYS AS (config_json::jsonb) STORED,
 status text NOT NULL CHECK(status IN ('running','waiting_confirmation','completed','stopped','unknown','limited','failed','abandoned')),
 stop_reason text NOT NULL,steps integer NOT NULL DEFAULT 0 CHECK(steps>=0),
 elapsed_ms bigint NOT NULL DEFAULT 0 CHECK(elapsed_ms>=0),output text NOT NULL,
 created_at text NOT NULL,updated_at text NOT NULL,active_since text NOT NULL DEFAULT '',
 seq bigint NOT NULL,last_attempt_seq bigint NOT NULL DEFAULT 0,
 UNIQUE(conversation_id,request_key),UNIQUE(conversation_id,seq),UNIQUE(id,conversation_id)
);
CREATE UNIQUE INDEX one_active_run ON fa.runs(conversation_id) WHERE
 (status IN ('running','waiting_confirmation','stopped','unknown') OR (status='limited' AND substr(stop_reason,1,14)!='settled_limit:'));
-- id is the existing public Message reference, now allocated locally within
-- its conversation. Every reference/query carries conversation_id or run_id.
CREATE TABLE fa.messages(
 id bigint NOT NULL,conversation_id text NOT NULL REFERENCES fa.conversations(id),
 run_id text,request_key text NOT NULL DEFAULT '',body_json jsonb NOT NULL,
 PRIMARY KEY(conversation_id,id),
 FOREIGN KEY(run_id,conversation_id) REFERENCES fa.runs(id,conversation_id)
);
CREATE UNIQUE INDEX channel_request_identity ON fa.messages(request_key) WHERE request_key!='';
CREATE INDEX messages_run ON fa.messages(run_id,id);
CREATE TABLE fa.attempts(
 id text PRIMARY KEY,run_id text NOT NULL REFERENCES fa.runs(id),
 kind text NOT NULL CHECK(kind IN ('model','tool','workflow')),call_id text NOT NULL,
 status text NOT NULL CHECK(status IN ('pending','dispatched','finished','unknown')),
 request_json text NOT NULL,request_doc jsonb GENERATED ALWAYS AS (request_json::jsonb) STORED,
 response_json text NOT NULL,response_doc jsonb GENERATED ALWAYS AS (fa.response_document(response_json)) STORED,
 seq bigint NOT NULL,UNIQUE(run_id,seq)
);
CREATE INDEX attempts_unknown ON fa.attempts(run_id) WHERE status='unknown';
CREATE TABLE fa.memories(
 id text PRIMARY KEY,scope text NOT NULL CHECK(scope IN ('private','shared','knowledge')),
 owner text NOT NULL,project text NOT NULL,task_id text NOT NULL,
 revision integer NOT NULL CHECK(revision>=1),state text NOT NULL CHECK(state IN ('active','disabled','deleted')),
 kind text NOT NULL,title text NOT NULL,body text NOT NULL,author text NOT NULL,sources_json jsonb NOT NULL,
 created_at text NOT NULL,updated_at text NOT NULL,pin_json jsonb NOT NULL DEFAULT 'null'
);
CREATE INDEX memory_scope ON fa.memories(scope,owner,project,task_id,state,updated_at);
CREATE INDEX memory_knowledge_trigram ON fa.memories USING gin (lower(body) public.gin_trgm_ops) WHERE scope='knowledge' AND state='active';
CREATE TABLE fa.memory_revisions(
 entry_id text NOT NULL REFERENCES fa.memories(id),revision integer NOT NULL CHECK(revision>=1),
 state text NOT NULL,kind text NOT NULL,title text NOT NULL,body text NOT NULL,author text NOT NULL,
 sources_json jsonb NOT NULL,reason text NOT NULL,created_at text NOT NULL,pin_json jsonb NOT NULL DEFAULT 'null',
 PRIMARY KEY(entry_id,revision)
);
CREATE TABLE fa.memory_operations(
 id text PRIMARY KEY,request_hash text NOT NULL,receipt_json jsonb NOT NULL,created_at text NOT NULL
);

CREATE FUNCTION fa.allocate_run() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE fa.conversations SET last_run_seq=last_run_seq+1 WHERE id=NEW.conversation_id RETURNING last_run_seq INTO NEW.seq;
 RETURN NEW;
END $$;
CREATE FUNCTION fa.note_maintenance_change() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF session_user=current_user AND pg_trigger_depth()=1 THEN
  UPDATE fa.meta SET value=jsonb_build_object('revision',COALESCE((document->>'revision')::bigint,0)+1)::text WHERE key='core/external';
 END IF;
 RETURN NULL;
END $$;
DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['meta','spaces','conversations','runs','messages','attempts','memories','memory_revisions','memory_operations'] LOOP
  EXECUTE format('CREATE TRIGGER z_maintenance_revision AFTER INSERT OR UPDATE OR DELETE ON fa.%I FOR EACH STATEMENT EXECUTE FUNCTION fa.note_maintenance_change()',n);
 END LOOP;
END $$;
CREATE TRIGGER b_run_sequence BEFORE INSERT ON fa.runs FOR EACH ROW EXECUTE FUNCTION fa.allocate_run();
CREATE FUNCTION fa.allocate_message() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' OR NEW.id IS DISTINCT FROM OLD.id THEN
  UPDATE fa.conversations SET last_message_seq=last_message_seq+1 WHERE id=NEW.conversation_id RETURNING last_message_seq INTO NEW.id;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER b_message_sequence BEFORE INSERT OR UPDATE OF id ON fa.messages FOR EACH ROW EXECUTE FUNCTION fa.allocate_message();
CREATE FUNCTION fa.allocate_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE fa.runs SET last_attempt_seq=last_attempt_seq+1 WHERE id=NEW.run_id RETURNING last_attempt_seq INTO NEW.seq;
 RETURN NEW;
END $$;
CREATE TRIGGER b_attempt_sequence BEFORE INSERT ON fa.attempts FOR EACH ROW EXECUTE FUNCTION fa.allocate_attempt();

-- The dedicated advisory lock connection alone may claim a generation.
CREATE FUNCTION fa.claim_owner() RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE g bigint;
BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=1178686216::oid AND objid=1::oid AND granted) THEN
  RAISE EXCEPTION 'FreeAgent Owner lock required' USING ERRCODE='55000';
 END IF;
 SELECT COALESCE((document->>'generation')::bigint,0)+1 INTO g FROM fa.meta WHERE key='core/owner' FOR UPDATE;
 g:=COALESCE(g,1);
 INSERT INTO fa.meta(key,value) VALUES('core/owner',jsonb_build_object('generation',g,'pid',pg_backend_pid())::text)
 ON CONFLICT(key) DO UPDATE SET value=excluded.value;
 RETURN g::text;
END $$;
CREATE FUNCTION fa.fence_writer() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE owner_document jsonb;
BEGIN
 -- Administrative maintenance runs explicitly, under the OS maintenance lock.
 IF session_user=current_user OR EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=1178686216::oid AND objid=1::oid AND granted) THEN RETURN NULL; END IF;
 SELECT document INTO owner_document FROM fa.meta WHERE key='core/owner' FOR SHARE;
 IF owner_document->>'generation' IS DISTINCT FROM current_setting('freeagent.generation',true)
 OR NOT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=(owner_document->>'pid')::int AND classid=1178686216::oid AND objid=1::oid AND granted) THEN
  RAISE EXCEPTION 'FreeAgent Owner generation lost; dispatch and writes stopped' USING ERRCODE='55000';
 END IF;
 RETURN NULL;
END $$;
CREATE FUNCTION fa.protect_identity() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 IF session_user<>current_user AND (COALESCE(OLD.key,NEW.key) IN ('format','core/id','core/owner') OR COALESCE(OLD.key,NEW.key) LIKE 'schema/%') AND NOT
 (COALESCE(OLD.key,NEW.key)='core/owner' AND EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=1178686216::oid AND objid=1::oid AND granted)) THEN
  RAISE EXCEPTION 'FreeAgent identity is maintenance-only' USING ERRCODE='42501';
 END IF;
 RETURN COALESCE(NEW,OLD);
END $$;
CREATE TRIGGER a_protect_identity BEFORE INSERT OR UPDATE OR DELETE ON fa.meta FOR EACH ROW EXECUTE FUNCTION fa.protect_identity();
DO $$ DECLARE n text; BEGIN
 FOREACH n IN ARRAY ARRAY['meta','spaces','conversations','runs','messages','attempts','memories','memory_revisions','memory_operations'] LOOP
  EXECUTE format('CREATE TRIGGER a_owner_fence BEFORE INSERT OR UPDATE OR DELETE ON fa.%I FOR EACH STATEMENT EXECUTE FUNCTION fa.fence_writer()',n);
 END LOOP;
END $$;
