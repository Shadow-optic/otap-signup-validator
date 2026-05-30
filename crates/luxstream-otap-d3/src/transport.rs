//! Tokio async transport for the D3 handshake.
//!
//! Drives the typed state machine from `handshake.rs` over TCP sockets.
//! All message types use the Hs-prefixed wire format with encode/decode.

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;

use crate::handshake::{
    ClientAcl, HsError, HsMsg1, HsMsg2, HsMsg3, Initiator, Responder, MSG1_LEN, MSG2_LEN, MSG3_LEN,
};
use crate::key_domain::{D3SigningKey, D3VerifyingKey};
use crate::session::D3Session;

/// Run the initiator handshake over a TCP stream.
/// Returns a ready-to-use D3Session on success.
pub async fn run_initiator(
    mut stream: TcpStream,
    client_sk: D3SigningKey,
    server_pk: D3VerifyingKey,
) -> Result<D3Session, HsError> {
    // 1. Generate and send Msg1
    let (wait_msg2, msg1) = Initiator::new(client_sk.clone(), server_pk).start();
    stream
        .write_all(&msg1.encode())
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;

    // 2. Receive Msg2
    let mut buf2 = [0u8; MSG2_LEN];
    stream
        .read_exact(&mut buf2)
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;
    let msg2 = HsMsg2::decode(&buf2)?;

    // 3. Verify Msg2, generate Msg3
    let (established, msg3) = wait_msg2.finish(msg2)?;
    stream
        .write_all(&msg3.encode())
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;

    Ok(D3Session::from_client_handshake(established, client_sk))
}

/// Run the responder handshake over a TCP stream.
/// Returns a ready-to-use D3Session on success.
pub async fn run_responder<A: ClientAcl>(
    mut stream: TcpStream,
    server_sk: D3SigningKey,
    acl: &A,
) -> Result<D3Session, HsError> {
    // 1. Receive Msg1
    let mut buf1 = [0u8; MSG1_LEN];
    stream
        .read_exact(&mut buf1)
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;
    let msg1 = HsMsg1::decode(&buf1)?;

    // 2. ACL check + generate Msg2
    let responder = Responder::new(server_sk.clone(), acl);
    let (wait_msg3, msg2) = responder.handle_msg1(msg1)?;
    stream
        .write_all(&msg2.encode())
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;

    // 3. Receive Msg3
    let mut buf3 = [0u8; MSG3_LEN];
    stream
        .read_exact(&mut buf3)
        .await
        .map_err(|e| HsError::Transport(e.to_string()))?;
    let msg3 = HsMsg3::decode(&buf3)?;

    // 4. Verify Msg3
    let established = wait_msg3.handle_msg3(msg3)?;

    Ok(D3Session::from_server_handshake(established, server_sk))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::handshake::AllowAll;
    use crate::key_domain::PUBKEY_LEN;
    use crate::rpc::{Polarization, RpcDomain, Symbol};
    use tokio::net::TcpListener;

    struct PubkeyAcl {
        allowed: [u8; PUBKEY_LEN],
    }

    impl ClientAcl for PubkeyAcl {
        fn is_authorized(&self, pk: &[u8; PUBKEY_LEN]) -> bool {
            pk == &self.allowed
        }
    }

    #[tokio::test]
    async fn handshake_over_tcp_then_seal_open() {
        let client_sk = D3SigningKey::from_bytes(&[1u8; 32]);
        let server_sk = D3SigningKey::from_bytes(&[2u8; 32]);
        let server_pk = server_sk.verifying_key();

        let acl = PubkeyAcl {
            allowed: client_sk.verifying_key().as_bytes(),
        };

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();

        // Server
        let server_handle = tokio::spawn({
            let server_sk = server_sk.clone();
            async move {
                let (stream, _) = listener.accept().await.unwrap();
                run_responder(stream, server_sk, &acl).await
            }
        });

        // Client
        let client_stream = TcpStream::connect(addr).await.unwrap();
        let client_session = run_initiator(client_stream, client_sk, server_pk)
            .await
            .unwrap();

        let server_session = server_handle.await.unwrap().unwrap();

        // Both sides derived the same epoch nonce
        assert_eq!(client_session.epoch_nonce, server_session.epoch_nonce);

        // Data exchange works across the session boundary
        let payload = vec![Symbol {
            value: 777,
            lambda: 34,
            pol: Polarization::D,
        }];
        let frame = client_session.seal(RpcDomain::LinkData, payload);
        let opened = server_session.open(RpcDomain::LinkData, &frame).unwrap();
        assert_eq!(opened[0].value, 777);
    }

    #[tokio::test]
    async fn unauthorized_client_rejected() {
        let client_sk = D3SigningKey::from_bytes(&[1u8; 32]);
        let server_sk = D3SigningKey::from_bytes(&[2u8; 32]);
        let server_pk = server_sk.verifying_key();

        // ACL allows a different key
        let acl = PubkeyAcl {
            allowed: [0xFF; PUBKEY_LEN],
        };

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();

        let server_handle = tokio::spawn(async move {
            let (stream, _) = listener.accept().await.unwrap();
            run_responder(stream, server_sk, &acl).await
        });

        let client_stream = TcpStream::connect(addr).await.unwrap();
        // Client sends Msg1 fine, but server rejects based on ACL
        let _ = run_initiator(client_stream, client_sk, server_pk).await;

        let server_result = server_handle.await.unwrap();
        assert!(server_result.is_err());
    }
}
