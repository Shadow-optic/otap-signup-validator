package api

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	_ "github.com/otap/flr/internal/audit" // imported for future audit hooks; intentionally unused
	"github.com/otap/flr/internal/config"
	"github.com/otap/flr/internal/crypto"
	"github.com/otap/flr/internal/federation"
	"github.com/otap/flr/internal/models"
	"github.com/otap/flr/internal/registry"
	"github.com/otap/flr/internal/xlat"
	flrv1 "github.com/otap/flr/proto/flr/v1"
)

// Server is the FLR API server
type Server struct {
	flrv1.UnimplementedFederatedRegistryServer

	grpcServer *grpc.Server
	httpServer *http.Server
	gateway    *runtime.ServeMux
	registry   *registry.Engine
	federation *federation.Manager
	xlatMgr    *xlat.Manager
	crypto     *crypto.Engine
	cfg        *config.Config
	logger     *slog.Logger
}

// NewServer creates the API server
func NewServer(cfg *config.Config, reg *registry.Engine, fed *federation.Manager, xlatMgr *xlat.Manager, crypt *crypto.Engine, logger *slog.Logger) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if reg == nil {
		return nil, fmt.Errorf("registry engine is required")
	}
	if fed == nil {
		return nil, fmt.Errorf("federation manager is required")
	}
	if xlatMgr == nil {
		return nil, fmt.Errorf("translation manager is required")
	}
	if crypt == nil {
		return nil, fmt.Errorf("crypto engine is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		registry:   reg,
		federation: fed,
		xlatMgr:    xlatMgr,
		crypto:     crypt,
		cfg:        cfg,
		logger:     logger.With("component", "api-server"),
	}, nil
}

// Start begins serving gRPC and HTTP
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting API server",
		"grpc_addr", s.cfg.Server.GRPCAddr,
		"http_addr", s.cfg.Server.HTTPAddr,
	)

	// Build interceptors
	interceptors := []grpc.UnaryServerInterceptor{
		loggingInterceptor(s.logger),
		recoveryInterceptor(s.logger),
	}

	if s.cfg.Server.EnableAuth {
		interceptors = append(interceptors, authInterceptor(nil))
	}

	if s.cfg.Server.MaxConn > 0 {
		interceptors = append(interceptors, rateLimitInterceptor(s.cfg.Server.MaxConn))
	}

	chain := grpc.ChainUnaryInterceptor(interceptors...)
	s.grpcServer = grpc.NewServer(chain)
	flrv1.RegisterFederatedRegistryServer(s.grpcServer, s)
	reflection.Register(s.grpcServer)

	// Start gRPC listener
	lis, err := net.Listen("tcp", s.cfg.Server.GRPCAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC addr %s: %w", s.cfg.Server.GRPCAddr, err)
	}

	go func() {
		s.logger.Info("gRPC server listening", "addr", s.cfg.Server.GRPCAddr)
		if err := s.grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			s.logger.Error("gRPC server error", "error", err)
		}
	}()

	// Setup HTTP gateway
	gateway, err := s.setupGateway(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup gateway: %w", err)
	}
	s.gateway = gateway

	// Combined HTTP mux: schema routes (additive) + gateway (catch-all).
	// The /v1/schemas... routes are handled by schema_rest.go; everything
	// else falls through to the protoc-generated grpc-gateway.
	httpMux := http.NewServeMux()
	s.RegisterSchemaRoutes(httpMux)
	httpMux.Handle("/", s.gateway)

	s.httpServer = &http.Server{
		Addr:         s.cfg.Server.HTTPAddr,
		Handler:      httpMux,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
	}

	go func() {
		s.logger.Info("HTTP server listening", "addr", s.cfg.Server.HTTPAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	return nil
}

// Shutdown gracefully stops the servers
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down API server")

	shutdownErrs := make([]error, 0, 2)

	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
		s.logger.Info("gRPC server stopped")
	}

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("HTTP shutdown error: %w", err))
		} else {
			s.logger.Info("HTTP server stopped")
		}
	}

	if len(shutdownErrs) > 0 {
		return shutdownErrs[0]
	}
	return nil
}

// setupGateway creates and configures the gRPC-gateway
func (s *Server) setupGateway(ctx context.Context) (*runtime.ServeMux, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
	)

	if err := flrv1.RegisterFederatedRegistryHandlerServer(ctx, mux, s); err != nil {
		return nil, fmt.Errorf("failed to register gateway handlers: %w", err)
	}

	return mux, nil
}

// ===== Lease Methods =====

// CreateLease creates a new wavelength lease
func (s *Server) CreateLease(ctx context.Context, req *flrv1.CreateLeaseRequest) (*flrv1.Lease, error) {
	s.logger.Info("CreateLease called", "endpoint_id", req.GetEndpointId(), "operator_id", req.GetOperatorId())

	if req.GetWavelength() == nil {
		return nil, status.Error(codes.InvalidArgument, "wavelength is required")
	}
	if req.GetEndpointId() == "" {
		return nil, status.Error(codes.InvalidArgument, "endpoint_id is required")
	}

	duration := 24 * time.Hour
	if req.GetDuration() != nil {
		duration = req.GetDuration().AsDuration()
	}

	wavelength := &models.Wavelength{
		LambdaNm:   req.GetWavelength().GetLambdaNm(),
		ChannelNum: req.GetWavelength().GetChannelNum(),
		Band:       models.Band(req.GetWavelength().GetBand()),
		GridGHz:    req.GetWavelength().GetGridGHz(),
	}

	lease, token, err := s.registry.AllocateLease(wavelength, req.GetEndpointId(), duration)
	if err != nil {
		s.logger.Error("CreateLease failed", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create lease: %v", err)
	}

	s.logger.Info("CreateLease succeeded", "lease_id", lease.ID)
	_ = token // token is stored/generated but not returned in Lease response

	return leaseToProto(lease), nil
}

// GetLease retrieves a lease by ID
func (s *Server) GetLease(ctx context.Context, req *flrv1.GetLeaseRequest) (*flrv1.Lease, error) {
	s.logger.Info("GetLease called", "lease_id", req.GetLeaseId())

	if req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
	}

	lease, err := s.registry.GetLease(req.GetLeaseId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "lease not found: %v", err)
	}

	return leaseToProto(lease), nil
}

// RenewLease extends an existing lease
func (s *Server) RenewLease(ctx context.Context, req *flrv1.RenewLeaseRequest) (*flrv1.Lease, error) {
	s.logger.Info("RenewLease called", "lease_id", req.GetLeaseId())

	if req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
	}

	extension := 24 * time.Hour
	if req.GetExtension() != nil {
		extension = req.GetExtension().AsDuration()
	}

	lease, _, err := s.registry.RenewLease(req.GetLeaseId(), extension)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to renew lease: %v", err)
	}

	return leaseToProto(lease), nil
}

// RevokeLease terminates a lease
func (s *Server) RevokeLease(ctx context.Context, req *flrv1.RevokeLeaseRequest) (*flrv1.Lease, error) {
	s.logger.Info("RevokeLease called", "lease_id", req.GetLeaseId())

	if req.GetLeaseId() == "" {
		return nil, status.Error(codes.InvalidArgument, "lease_id is required")
	}

	lease, err := s.registry.GetLease(req.GetLeaseId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "lease not found: %v", err)
	}

	if err := s.registry.RevokeLease(req.GetLeaseId()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to revoke lease: %v", err)
	}

	// Refresh lease state
	lease.Status = models.LeaseStatusRevoked
	lease.UpdatedAt = time.Now().UTC()

	return leaseToProto(lease), nil
}

// ListLeases lists all leases with optional filtering
func (s *Server) ListLeases(ctx context.Context, req *flrv1.ListLeasesRequest) (*flrv1.ListLeasesResponse, error) {
	s.logger.Info("ListLeases called", "operator_id", req.GetOperatorId(), "endpoint_id", req.GetEndpointId())

	filter := registry.LeaseFilter{
		OperatorID: req.GetOperatorId(),
		EndpointID: req.GetEndpointId(),
		Status:     models.LeaseStatus(req.GetStatus()),
	}

	leases, err := s.registry.ListLeases(filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list leases: %v", err)
	}

	result := make([]*flrv1.Lease, 0, len(leases))
	for _, l := range leases {
		result = append(result, leaseToProto(l))
	}

	return &flrv1.ListLeasesResponse{
		Leases: result,
		Total:  int32(len(result)),
	}, nil
}

// ===== Merkle Commitment & Verification =====

// GetMerkleCommitment retrieves a commitment by operator and block height
func (s *Server) GetMerkleCommitment(ctx context.Context, req *flrv1.GetMerkleCommitmentRequest) (*flrv1.MerkleCommitment, error) {
	s.logger.Info("GetMerkleCommitment called", "operator_id", req.GetOperatorId(), "block_height", req.GetBlockHeight())

	operatorID := req.GetOperatorId()
	if operatorID == "" {
		operatorID = s.cfg.Node.ID
	}

	var commitment *models.MerkleCommitment
	var err error

	if req.GetBlockHeight() > 0 {
		commitment, err = s.registry.GetCommitment(operatorID, req.GetBlockHeight())
	} else {
		commitment, err = s.registry.GetLatestCommitment()
	}

	if err != nil {
		return nil, status.Errorf(codes.NotFound, "commitment not found: %v", err)
	}

	return commitmentToProto(commitment), nil
}

// VerifyLease checks a lease token against stored state
func (s *Server) VerifyLease(ctx context.Context, req *flrv1.VerifyLeaseRequest) (*flrv1.VerificationResult, error) {
	s.logger.Info("VerifyLease called", "lease_id", req.GetToken().GetLeaseId())

	if req.GetToken() == nil {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}

	token := leaseTokenFromProto(req.GetToken())
	result, err := s.registry.VerifyLease(token)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "verification failed: %v", err)
	}

	return &flrv1.VerificationResult{
		Valid:       result.Valid,
		Reason:      result.Reason,
		ComputedHash: result.ComputedHash,
		StoredHash:  result.StoredHash,
	}, nil
}

// SubmitProofOfInvalidity processes a proof of invalidity
func (s *Server) SubmitProofOfInvalidity(ctx context.Context, req *flrv1.SubmitProofOfInvalidityRequest) (*flrv1.InvalidityResult, error) {
	s.logger.Info("SubmitProofOfInvalidity called", "type", req.GetType())

	invType := models.InvalidityUnspecified
	switch req.GetType() {
	case "double_allocation":
		invType = models.InvalidityDoubleAllocation
	case "expired_lease":
		invType = models.InvalidityExpiredLease
	case "invalid_signature":
		invType = models.InvalidityInvalidSignature
	case "unauthorized_op":
		invType = models.InvalidityUnauthorizedOp
	}

	poi := &models.ProofOfInvalidity{
		Type:        invType,
		MerkleProof: req.GetMerkleProof(),
		Timestamp:   time.Now().UTC(),
	}

	if err := s.federation.HandleProofOfInvalidity(poi); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to process proof: %v", err)
	}

	return &flrv1.InvalidityResult{
		Accepted:   true,
		Resolution: fmt.Sprintf("proof accepted: type=%s", invType.String()),
	}, nil
}

// ===== Operator Methods =====

// RegisterOperator registers a new peer operator
func (s *Server) RegisterOperator(ctx context.Context, req *flrv1.RegisterOperatorRequest) (*flrv1.Operator, error) {
	s.logger.Info("RegisterOperator called", "id", req.GetId(), "name", req.GetName())

	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "operator id is required")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "operator name is required")
	}

	op := &models.Operator{
		ID:        req.GetId(),
		Name:      req.GetName(),
		PublicKey: req.GetPublicKey(),
		Endpoint:  req.GetEndpoint(),
		Status:    models.OperatorStatusActive,
		JoinedAt:  time.Now().UTC(),
		LastSeen:  time.Now().UTC(),
	}

	if err := s.registry.CreateOperator(op); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register operator: %v", err)
	}
	if err := s.federation.RegisterOperator(op); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register in federation: %v", err)
	}

	return operatorToProto(op), nil
}

// GetOperator retrieves an operator by ID
func (s *Server) GetOperator(ctx context.Context, req *flrv1.GetOperatorRequest) (*flrv1.Operator, error) {
	s.logger.Info("GetOperator called", "operator_id", req.GetOperatorId())

	if req.GetOperatorId() == "" {
		return nil, status.Error(codes.InvalidArgument, "operator_id is required")
	}

	op, err := s.registry.GetOperator(req.GetOperatorId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "operator not found: %v", err)
	}

	return operatorToProto(op), nil
}

// ListOperators lists all registered operators
func (s *Server) ListOperators(ctx context.Context, req *flrv1.ListOperatorsRequest) (*flrv1.ListOperatorsResponse, error) {
	s.logger.Info("ListOperators called")

	ops, err := s.registry.ListOperators()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list operators: %v", err)
	}

	var result []*flrv1.Operator
	for _, op := range ops {
		if req.GetStatus() != 0 && op.Status != models.OperatorStatus(req.GetStatus()) {
			continue
		}
		result = append(result, operatorToProto(op))
	}

	return &flrv1.ListOperatorsResponse{Operators: result}, nil
}

// ===== Translation Methods =====

// CreateTranslation creates a cross-operator translation
func (s *Server) CreateTranslation(ctx context.Context, req *flrv1.CreateTranslationRequest) (*flrv1.TranslationEntry, error) {
	s.logger.Info("CreateTranslation called",
		"from_op", req.GetFromOperator(),
		"to_op", req.GetToOperator())

	if req.GetFromOperator() == nil || req.GetToOperator() == nil {
		return nil, status.Error(codes.InvalidArgument, "from_operator and to_operator wavelength info required")
	}

	duration := 24 * time.Hour
	if req.GetDuration() != nil {
		duration = req.GetDuration().AsDuration()
	}

	fromWL := &models.Wavelength{
		LambdaNm:   req.GetFromWavelength().GetLambdaNm(),
		ChannelNum: req.GetFromWavelength().GetChannelNum(),
		Band:       models.Band(req.GetFromWavelength().GetBand()),
		GridGHz:    req.GetFromWavelength().GetGridGHz(),
	}
	toWL := &models.Wavelength{
		LambdaNm:   req.GetToWavelength().GetLambdaNm(),
		ChannelNum: req.GetToWavelength().GetChannelNum(),
		Band:       models.Band(req.GetToWavelength().GetBand()),
		GridGHz:    req.GetToWavelength().GetGridGHz(),
	}

	entry, err := s.xlatMgr.CreateTranslation(
		req.GetFromOperator(), req.GetToOperator(),
		fromWL, toWL,
		req.GetFromAwgPort(), req.GetToAwgPort(),
		duration,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create translation: %v", err)
	}

	return translationToProto(entry), nil
}

// GetTranslation retrieves a translation by ID
func (s *Server) GetTranslation(ctx context.Context, req *flrv1.GetTranslationRequest) (*flrv1.TranslationEntry, error) {
	s.logger.Info("GetTranslation called", "translation_id", req.GetTranslationId())

	if req.GetTranslationId() == "" {
		return nil, status.Error(codes.InvalidArgument, "translation_id is required")
	}

	entry, err := s.xlatMgr.GetTranslation(req.GetTranslationId())
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "translation not found: %v", err)
	}

	return translationToProto(entry), nil
}

// ListTranslations lists translations with optional filtering
func (s *Server) ListTranslations(ctx context.Context, req *flrv1.ListTranslationsRequest) (*flrv1.ListTranslationsResponse, error) {
	s.logger.Info("ListTranslations called")

	filter := xlat.TranslationFilter{
		FromOperator: req.GetFromOperator(),
		ToOperator:   req.GetToOperator(),
		Status:       models.TranslationStatus(req.GetStatus()),
	}

	entries, err := s.xlatMgr.ListTranslations(filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list translations: %v", err)
	}

	result := make([]*flrv1.TranslationEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, translationToProto(e))
	}

	return &flrv1.ListTranslationsResponse{
		Translations: result,
		Total:        int32(len(result)),
	}, nil
}

// StreamRegistryUpdates streams registry updates to clients
func (s *Server) StreamRegistryUpdates(req *flrv1.StreamRegistryUpdatesRequest, stream flrv1.FederatedRegistry_StreamRegistryUpdatesServer) error {
	s.logger.Info("StreamRegistryUpdates called", "operator_id", req.GetOperatorId())

	// Send a welcome update
	update := &flrv1.RegistryUpdate{
		Operation:   "connected",
		BlockHeight: 0,
		Timestamp:   timestamppb.Now(),
	}
	if err := stream.Send(update); err != nil {
		s.logger.Error("stream send error", "error", err)
		return err
	}

	// In a real implementation, this would subscribe to a pub/sub channel
	// For now, send periodic heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case t := <-ticker.C:
			update := &flrv1.RegistryUpdate{
				Operation:   "heartbeat",
				BlockHeight: 0,
				Timestamp:   timestamppb.New(t),
			}
			if err := stream.Send(update); err != nil {
				s.logger.Error("stream send error", "error", err)
				return err
			}
		}
	}
}

// ===== Model Converters =====

func leaseToProto(l *models.Lease) *flrv1.Lease {
	if l == nil {
		return nil
	}
	return &flrv1.Lease{
		Id:         l.ID,
		Wavelength: wavelengthToProto(l.Wavelength),
		OperatorId: l.OperatorID,
		Status:     flrv1.LeaseStatus(l.Status),
		StartTime:  timestamppb.New(l.StartTime),
		EndTime:    timestamppb.New(l.EndTime),
		CreatedAt:  timestamppb.New(l.CreatedAt),
		UpdatedAt:  timestamppb.New(l.UpdatedAt),
		TokenHash:  l.TokenHash,
		ParentHash: l.ParentHash,
	}
}

func wavelengthToProto(w *models.Wavelength) *flrv1.Wavelength {
	if w == nil {
		return nil
	}
	return &flrv1.Wavelength{
		LambdaNm:   w.LambdaNm,
		ChannelNum: w.ChannelNum,
		Band:       flrv1.Band(w.Band),
		GridGHz:    w.GridGHz,
	}
}

func commitmentToProto(c *models.MerkleCommitment) *flrv1.MerkleCommitment {
	if c == nil {
		return nil
	}
	return &flrv1.MerkleCommitment{
		OperatorId:  c.OperatorID,
		RootHash:    c.RootHash,
		Timestamp:   timestamppb.New(c.Timestamp),
		Signature:   c.Signature,
		LeaseCount:  c.LeaseCount,
		BlockHeight: c.BlockHeight,
	}
}

func operatorToProto(o *models.Operator) *flrv1.Operator {
	if o == nil {
		return nil
	}
	return &flrv1.Operator{
		Id:        o.ID,
		Name:      o.Name,
		PublicKey: o.PublicKey,
		Endpoint:  o.Endpoint,
		Status:    flrv1.OperatorStatus(o.Status),
		JoinedAt:  timestamppb.New(o.JoinedAt),
		LastSeen:  timestamppb.New(o.LastSeen),
	}
}

func translationToProto(t *models.TranslationEntry) *flrv1.TranslationEntry {
	if t == nil {
		return nil
	}
	return &flrv1.TranslationEntry{
		Id:             t.ID,
		FromOperator:   t.FromOperator,
		ToOperator:     t.ToOperator,
		FromWavelength: wavelengthToProto(t.FromWavelength),
		ToWavelength:   wavelengthToProto(t.ToWavelength),
		FromAwgPort:    t.FromAWGPort,
		ToAwgPort:      t.ToAWGPort,
		Status:         flrv1.TranslationStatus(t.Status),
		EffectiveTime:  timestamppb.New(t.EffectiveTime),
		ExpiryTime:     timestamppb.New(t.ExpiryTime),
	}
}

func leaseTokenFromProto(t *flrv1.LeaseToken) *models.LeaseToken {
	if t == nil {
		return nil
	}
	return &models.LeaseToken{
		Version:    t.GetVersion(),
		LeaseID:    t.GetLeaseId(),
		OperatorID: t.GetOperatorId(),
		Wavelength: &models.Wavelength{
			LambdaNm:   t.GetWavelength().GetLambdaNm(),
			ChannelNum: t.GetWavelength().GetChannelNum(),
			Band:       models.Band(t.GetWavelength().GetBand()),
			GridGHz:    t.GetWavelength().GetGridGHz(),
		},
		EndpointID: t.GetEndpointId(),
		Nonce:      t.GetNonce(),
		Signature:  t.GetSignature(),
	}
}
