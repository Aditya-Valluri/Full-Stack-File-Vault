import { useEffect, useState } from 'react';
import { FileSharesDocument, type FileSharesQuery } from '../generated/graphql';
import { query } from '../lib/api';
import { formatDate } from '../lib/format';

// Metadata only: all link creation, revocation and access controls stay in Share.
export function FileSharingSummary({ fileID }: { fileID: string }) {
 const [data, setData] = useState<FileSharesQuery['fileShares']>();
 const [failed, setFailed] = useState(false);
 useEffect(() => {
  let active = true;
  void query(FileSharesDocument, { id: fileID }).then(result => { if (active) setData(result.fileShares); }, () => { if (active) setFailed(true); });
  return () => { active = false; };
 }, [fileID]);
 if (failed) return <p className="help-text">Sharing metadata is temporarily unavailable.</p>;
 if (!data) return <p role="status">Loading sharing metadata…</p>;
 const permissions = [...new Set(data.shares.map(share => share.permission === 'DOWNLOAD' ? 'Download only' : share.permission === 'PREVIEW_ONLY' ? 'View only' : 'Preview and download'))];
 const nextExpiry = data.shares.map(share => share.expiresAt).sort((a, b) => Date.parse(a) - Date.parse(b))[0];
 return <dl>
  <dt>Active sharing links</dt><dd>{data.shares.length}</dd>
  <dt>Share modes</dt><dd>{permissions.join(', ') || 'Private — no active links'}</dd>
  <dt>Next link expiry</dt><dd>{nextExpiry ? formatDate(nextExpiry) : 'None'}</dd>
  <dt>Shared download starts</dt><dd>{data.downloadStarts} (not confirmed completed transfers)</dd>
  <dt>Most recent recorded share activity</dt><dd>{data.activity[0] ? formatDate(data.activity[0].occurredAt) : 'None recorded'}</dd>
 </dl>;
}
